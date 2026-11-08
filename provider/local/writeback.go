package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/provider/parser"
	E "github.com/sagernet/sing/common/exceptions"
	SJSON "github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/service/filemanager"
)

// writeOverrides persists changes to a sing-box JSON source without discarding
// unrelated fields or nodes that the subscription parser deliberately skips.
// Other formats are kept intact rather than silently converted or truncated.
func (s *ProviderLocal) writeOverrides(path string, source []byte, info os.FileInfo, outbounds []option.Outbound, endpoints []option.Endpoint) error {
	if s.overrideDialer == nil && s.overrideTLS == nil && s.overrideAnyTLS == nil && s.overrideTransport == nil {
		return nil
	}
	originalOutbounds, originalEndpoints, err := parser.ParseSubscription(s.ctx, string(source), nil, nil, nil, nil, s.Tag())
	if err != nil {
		return E.Cause(err, "parse original local provider for writeback")
	}
	if reflect.DeepEqual(originalOutbounds, outbounds) && reflect.DeepEqual(originalEndpoints, endpoints) {
		return nil
	}
	document, err := parseOrderedJSONObject(source)
	_, hasOutbounds := document.get("outbounds")
	_, hasEndpoints := document.get("endpoints")
	if err != nil || !json.Valid(source) || (!hasOutbounds && !hasEndpoints) || !uniqueJSONKeys(source) {
		s.logger.Warn("local provider overrides are applied in memory only: writing back requires a sing-box JSON subscription without duplicate keys")
		return nil
	}
	changedOutbounds, err := mergeSubscriptionNodes(s.ctx, &document, "outbounds", originalOutbounds, outbounds)
	if err != nil {
		return err
	}
	changedEndpoints, err := mergeSubscriptionNodes(s.ctx, &document, "endpoints", originalEndpoints, endpoints)
	if err != nil {
		return err
	}
	if !changedOutbounds && !changedEndpoints {
		return nil
	}
	updated, err := document.marshal()
	if err != nil {
		return err
	}
	var formatted bytes.Buffer
	if err = json.Indent(&formatted, updated, "", "  "); err != nil {
		return err
	}
	formatted.WriteByte('\n')
	return s.replaceLocalFile(path, source, info, formatted.Bytes())
}

// Re-encoding an object with duplicate keys would silently discard data.
func uniqueJSONKeys(source []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(source))
	decoder.UseNumber()
	var visit func() error
	visit = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delim, container := token.(json.Delim)
		if !container {
			return nil
		}
		switch delim {
		case '{':
			keys := make(map[string]bool)
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || keys[name] {
					return fmt.Errorf("duplicate JSON object key")
				}
				keys[name] = true
				if err = visit(); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err = visit(); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unexpected JSON delimiter")
		}
		_, err = decoder.Token()
		return err
	}
	return visit() == nil
}

func mergeSubscriptionNodes[T any](ctx context.Context, document *orderedJSONObject, field string, before, after []T) (bool, error) {
	if len(before) != len(after) {
		return false, fmt.Errorf("local provider %s changed node count", field)
	}
	if len(before) == 0 {
		return false, nil
	}
	var nodes []json.RawMessage
	rawNodes, _ := document.get(field)
	if err := json.Unmarshal(rawNodes, &nodes); err != nil {
		return false, E.Cause(err, "read local provider ", field)
	}
	index := 0
	changed := false
	for i, raw := range nodes {
		if field == "outbounds" {
			var node struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(raw, &node); err != nil {
				return false, err
			}
			switch node.Type {
			case C.TypeDirect, C.TypeBlock, C.TypeDNS, C.TypeSelector, C.TypeURLTest, C.TypePass, C.TypeSmartLoadBalance, C.TypeSmartRegion:
				continue
			}
		}
		if index >= len(before) {
			return false, fmt.Errorf("local provider %s node count mismatch", field)
		}
		oldJSON, err := SJSON.MarshalContext(ctx, &before[index])
		if err != nil {
			return false, err
		}
		newJSON, err := SJSON.MarshalContext(ctx, &after[index])
		if err != nil {
			return false, err
		}
		merged, nodeChanged, err := mergeOverrideJSON(raw, oldJSON, newJSON, false)
		if err != nil {
			return false, E.Cause(err, "merge local provider ", field, "[", i, "]")
		}
		if nodeChanged {
			nodes[i] = merged
			changed = true
		}
		index++
	}
	if index != len(before) {
		return false, fmt.Errorf("local provider %s node count mismatch", field)
	}
	if changed {
		encoded, err := json.Marshal(nodes)
		if err != nil {
			return false, err
		}
		document.set(field, encoded)
	}
	return changed, nil
}

// orderedJSONObject keeps the source order and raw values, including large
// numbers, without making assumptions about a node's known schema.
type orderedJSONField struct {
	key   string
	value json.RawMessage
}
type orderedJSONObject []orderedJSONField

func parseOrderedJSONObject(data []byte) (orderedJSONObject, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if token != json.Delim('{') {
		return nil, fmt.Errorf("expected JSON object")
	}
	var fields orderedJSONObject
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("expected JSON object key")
		}
		var value json.RawMessage
		if err = decoder.Decode(&value); err != nil {
			return nil, err
		}
		fields = append(fields, orderedJSONField{key: key, value: value})
	}
	_, err = decoder.Token()
	return fields, err
}

func (o orderedJSONObject) get(key string) (json.RawMessage, bool) {
	for _, field := range o {
		if field.key == key {
			return field.value, true
		}
	}
	return nil, false
}
func (o *orderedJSONObject) set(key string, value json.RawMessage) {
	for i := range *o {
		if (*o)[i].key == key {
			(*o)[i].value = value
			return
		}
	}
	*o = append(*o, orderedJSONField{key: key, value: value})
}
func (o orderedJSONObject) marshal() ([]byte, error) {
	var result bytes.Buffer
	result.WriteByte('{')
	for i, field := range o {
		if i > 0 {
			result.WriteByte(',')
		}
		key, err := json.Marshal(field.key)
		if err != nil {
			return nil, err
		}
		result.Write(key)
		result.WriteByte(':')
		result.Write(field.value)
	}
	result.WriteByte('}')
	return result.Bytes(), nil
}

// Only apply differences made by the override, not parser normalization, to
// the original JSON. Existing fields retain their relative order; new fields
// are appended in the order supplied by the encoder.
func mergeOverrideJSON(source, before, after json.RawMessage, isHeaders bool) (json.RawMessage, bool, error) {
	if bytes.Equal(before, after) || equivalentJSON(source, after) {
		return source, false, nil
	}
	original, sourceErr := parseOrderedJSONObject(source)
	var old, updated map[string]json.RawMessage
	afterFields, afterErr := parseOrderedJSONObject(after)
	if sourceErr != nil || afterErr != nil || json.Unmarshal(before, &old) != nil || old == nil || json.Unmarshal(after, &updated) != nil || updated == nil {
		return after, true, nil
	}
	changed := false
	mergedFields := make(orderedJSONObject, 0, len(original))
	for _, field := range original {
		oldValue, existedBefore := old[field.key]
		newValue, existsAfter := updated[field.key]
		if existedBefore && !existsAfter {
			changed = true
			continue
		}
		if existsAfter && ((!existedBefore && !equivalentJSON(field.value, newValue)) || (existedBefore && !bytes.Equal(oldValue, newValue))) {
			// Preserve the original list representation of Host when the
			// encoder uses a scalar for a single list element.
			if strings.EqualFold(field.key, "host") {
				newValue = preserveStringListShape(field.value, newValue)
			}
			if !existedBefore {
				oldValue = field.value
			}
			value, valueChanged, err := mergeOverrideJSON(field.value, oldValue, newValue, field.key == "headers")
			if err != nil {
				return nil, false, err
			}
			if valueChanged {
				field.value = value
				changed = true
			}
		}
		mergedFields = append(mergedFields, field)
	}
	for _, field := range afterFields {
		if _, exists := original.get(field.key); exists {
			continue
		}
		oldValue, existedBefore := old[field.key]
		if !existedBefore || !bytes.Equal(oldValue, field.value) {
			mergedFields = append(mergedFields, field)
			changed = true
		}
	}
	if !changed {
		return source, false, nil
	}
	if isHeaders {
		// With-At is a special local CONNECT option, which is deliberately
		// written before other headers while retaining their relative order.
		ordered := make(orderedJSONObject, 0, len(mergedFields))
		for _, field := range mergedFields {
			if strings.EqualFold(field.key, "With-At") {
				ordered = append(ordered, field)
			}
		}
		for _, field := range mergedFields {
			if !strings.EqualFold(field.key, "With-At") {
				ordered = append(ordered, field)
			}
		}
		mergedFields = ordered
	}
	result, err := mergedFields.marshal()
	return result, true, err
}

func preserveStringListShape(source, value json.RawMessage) json.RawMessage {
	var original []string
	var replacement string
	if json.Unmarshal(source, &original) != nil || original == nil || json.Unmarshal(value, &replacement) != nil {
		return value
	}
	encoded, err := json.Marshal([]string{replacement})
	if err != nil {
		return value
	}
	return encoded
}

// Compare decoded values without converting large numbers to float64.
func equivalentJSON(a, b []byte) bool {
	if bytes.Equal(a, b) {
		return true
	}
	decode := func(data []byte) (any, error) {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		var value any
		err := decoder.Decode(&value)
		return value, err
	}
	left, err := decode(a)
	if err != nil {
		return false
	}
	right, err := decode(b)
	return err == nil && reflect.DeepEqual(left, right)
}

// Write to a sibling temporary file before replacing the source, and refuse to
// overwrite a file changed by another writer while we were preparing the data.
func (s *ProviderLocal) replaceLocalFile(path string, source []byte, info os.FileInfo, updated []byte) error {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(resolved), ".sing-box-provider-*")
	if err != nil {
		return E.Cause(err, "create local provider temporary file")
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = preserveLocalOwner(tmp, info); err == nil {
		err = tmp.Chmod(info.Mode() & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky))
	}
	if err == nil {
		_, err = tmp.Write(updated)
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil {
		return E.Cause(err, "write local provider temporary file")
	}
	if closeErr != nil {
		return closeErr
	}
	current, err := filemanager.Open(s.ctx, path)
	if err != nil {
		return err
	}
	currentInfo, statErr := current.Stat()
	currentContent, readErr := io.ReadAll(current)
	closeErr = current.Close()
	if statErr != nil {
		return statErr
	}
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	currentResolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	if currentResolved != resolved || !os.SameFile(info, currentInfo) || !bytes.Equal(source, currentContent) {
		return E.New("local provider file changed during override writeback; refusing to overwrite")
	}
	if err = filemanager.Rename(s.ctx, name, resolved); err != nil {
		return E.Cause(err, "replace local provider file")
	}
	return nil
}
