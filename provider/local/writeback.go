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
	"sort"
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
	var document map[string]json.RawMessage
	if err := json.Unmarshal(source, &document); err != nil || document == nil || (document["outbounds"] == nil && document["endpoints"] == nil) || !uniqueJSONKeys(source) {
		s.logger.Warn("local provider overrides are applied in memory only: writing back requires a sing-box JSON subscription without duplicate keys")
		return nil
	}
	changedOutbounds, err := mergeSubscriptionNodes(s.ctx, document, "outbounds", originalOutbounds, outbounds)
	if err != nil {
		return err
	}
	changedEndpoints, err := mergeSubscriptionNodes(s.ctx, document, "endpoints", originalEndpoints, endpoints)
	if err != nil {
		return err
	}
	if !changedOutbounds && !changedEndpoints {
		return nil
	}
	updated, err := marshalRawObject(document)
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

func mergeSubscriptionNodes[T any](ctx context.Context, document map[string]json.RawMessage, field string, before, after []T) (bool, error) {
	if len(before) != len(after) {
		return false, fmt.Errorf("local provider %s changed node count", field)
	}
	if len(before) == 0 {
		return false, nil
	}
	var nodes []json.RawMessage
	if err := json.Unmarshal(document[field], &nodes); err != nil {
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
		merged, nodeChanged, err := mergeOverrideJSON(raw, oldJSON, newJSON)
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
		document[field] = encoded
	}
	return changed, nil
}

// Only apply differences made by the override, not parser normalization, to
// the original JSON. This also leaves unrecognized fields intact.
func mergeOverrideJSON(source, before, after json.RawMessage) (json.RawMessage, bool, error) {
	if bytes.Equal(before, after) || equivalentJSON(source, after) {
		return source, false, nil
	}
	var original, old, updated map[string]json.RawMessage
	if json.Unmarshal(source, &original) != nil || original == nil || json.Unmarshal(before, &old) != nil || old == nil || json.Unmarshal(after, &updated) != nil || updated == nil {
		return after, true, nil
	}
	changed := false
	for key, oldValue := range old {
		newValue, exists := updated[key]
		if !exists {
			if _, present := original[key]; present {
				delete(original, key)
				changed = true
			}
			continue
		}
		if bytes.Equal(oldValue, newValue) {
			continue
		}
		originalValue, exists := original[key]
		if !exists {
			original[key] = newValue
			changed = true
			continue
		}
		merged, valueChanged, err := mergeOverrideJSON(originalValue, oldValue, newValue)
		if err != nil {
			return nil, false, err
		}
		if valueChanged {
			original[key] = merged
			changed = true
		}
	}
	for key, value := range updated {
		if _, exists := old[key]; !exists {
			if existing, present := original[key]; !present || !equivalentJSON(existing, value) {
				original[key] = value
				changed = true
			}
		}
	}
	if !changed {
		return source, false, nil
	}
	result, err := marshalRawObject(original)
	return result, true, err
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

func marshalRawObject(object map[string]json.RawMessage) ([]byte, error) {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		iAt, jAt := strings.EqualFold(keys[i], "With-At"), strings.EqualFold(keys[j], "With-At")
		if iAt != jAt {
			return iAt
		}
		return keys[i] < keys[j]
	})
	var result bytes.Buffer
	result.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			result.WriteByte(',')
		}
		encodedKey, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		result.Write(encodedKey)
		result.WriteByte(':')
		result.Write(object[key])
	}
	result.WriteByte('}')
	return result.Bytes(), nil
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
