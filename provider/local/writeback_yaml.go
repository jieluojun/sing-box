package provider

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/provider/parser"
	E "github.com/sagernet/sing/common/exceptions"
	"gopkg.in/yaml.v3"
)

// writeClashOverrides updates only the three fields with a supported mapping
// back to Clash YAML. It leaves the original representation and other fields
// intact rather than serializing the converted sing-box nodes.
func (s *ProviderLocal) writeClashOverrides(path string, source []byte, info os.FileInfo, outbounds []option.Outbound, endpoints []option.Endpoint) (bool, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(source))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil || len(document.Content) != 1 {
		return false, nil
	}
	root := document.Content[0]
	proxies, _ := yamlField(root, "proxies", false)
	if proxies == nil || proxies.Kind != yaml.SequenceNode {
		return false, nil
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		s.logger.Warn("local Clash YAML overrides are applied in memory only: multiple YAML documents cannot safely be rewritten")
		return true, nil
	}
	if !safeClashYAML(&document) {
		s.logger.Warn("local Clash YAML overrides are applied in memory only: aliases, merge keys, duplicate or complex keys cannot safely be rewritten")
		return true, nil
	}
	if s.hasOtherClashOverrides() {
		s.logger.Warn("local Clash YAML only writes back host, path and with_at; other overrides apply in memory only")
	}
	if s.overrideDialer == nil || (s.overrideDialer.Host == nil && s.overrideDialer.Path == nil && s.overrideDialer.WithAt == nil) {
		return true, nil
	}
	changed := false
	for _, node := range proxies.Content {
		if node.Kind != yaml.MappingNode {
			continue
		}
		var proxy parser.ClashProxy
		if err := node.Decode(&proxy); err != nil {
			return true, E.Cause(err, "read Clash proxy for writeback")
		}
		if proxy.Unsupported != "" || proxy.SingType == "" {
			continue
		}
		nodeChanged, err := applyClashOverride(node, proxy.Type, s.overrideDialer)
		if err != nil {
			return true, E.Cause(err, "write Clash proxy ", proxy.Name)
		}
		changed = changed || nodeChanged
	}
	if !changed {
		return true, nil
	}
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return true, E.Cause(err, "encode local Clash YAML")
	}
	if err := encoder.Close(); err != nil {
		return true, err
	}
	// Refuse to replace the user's file if encoding changed the effective
	// converted nodes for any reason (including unsupported YAML constructs).
	checkOut, checkEndpoints, err := parser.ParseSubscription(s.ctx, output.String(), s.overrideDialer, s.overrideTLS, s.overrideAnyTLS, s.overrideTransport, s.Tag())
	if err != nil {
		return true, E.Cause(err, "verify local Clash YAML writeback")
	}
	if !reflect.DeepEqual(outbounds, checkOut) || !reflect.DeepEqual(endpoints, checkEndpoints) {
		return true, E.New("refusing local Clash YAML writeback: converted node options changed")
	}
	return true, s.replaceLocalFile(path, source, info, output.Bytes())
}

func (s *ProviderLocal) hasOtherClashOverrides() bool {
	if s.overrideTLS != nil && !reflect.DeepEqual(*s.overrideTLS, option.OverrideTLSOptions{}) {
		return true
	}
	if s.overrideAnyTLS != nil && !reflect.DeepEqual(*s.overrideAnyTLS, option.OverrideAnyTLSOptions{}) {
		return true
	}
	if s.overrideTransport != nil && !reflect.DeepEqual(*s.overrideTransport, option.OverrideTransportOptions{}) {
		return true
	}
	if s.overrideDialer != nil {
		other := *s.overrideDialer
		other.Host, other.Path, other.WithAt = nil, nil, nil
		return !reflect.DeepEqual(other, option.OverrideDialerOptions{})
	}
	return false
}

func safeClashYAML(node *yaml.Node) bool {
	if node.Kind == yaml.AliasNode || node.Anchor != "" {
		return false
	}
	if node.Kind == yaml.MappingNode {
		keys := make(map[string]bool)
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || key.Value == "<<" || keys[key.Value] {
				return false
			}
			keys[key.Value] = true
		}
	}
	for _, child := range node.Content {
		if !safeClashYAML(child) {
			return false
		}
	}
	return true
}

func yamlField(mapping *yaml.Node, key string, fold bool) (*yaml.Node, int) {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil, -1
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		name := mapping.Content[i].Value
		if name == key || (fold && strings.EqualFold(name, key)) {
			return mapping.Content[i+1], i
		}
	}
	return nil, -1
}

func yamlEnsureMapping(parent *yaml.Node, key string) (*yaml.Node, error) {
	value, _ := yamlField(parent, key, false)
	if value != nil {
		if value.Kind == yaml.MappingNode {
			return value, nil
		}
		if value.Kind != yaml.ScalarNode || value.Tag != "!!null" {
			return nil, fmt.Errorf("%s must be a mapping", key)
		}
		value.Kind, value.Tag, value.Value = yaml.MappingNode, "!!map", ""
		if parent.Style&yaml.FlowStyle != 0 {
			value.Style = yaml.FlowStyle
		}
		return value, nil
	}
	value = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	if parent.Style&yaml.FlowStyle != 0 {
		value.Style = yaml.FlowStyle
	}
	parent.Content = append(parent.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
	return value, nil
}

func yamlString(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

// setYAMLString updates an existing scalar or list in place, keeping its style
// and key position. list requests a list for new fields (HTTP and H2 hosts).
func setYAMLString(mapping *yaml.Node, key, value string, fold, list bool) bool {
	node, index := yamlField(mapping, key, fold)
	if node == nil {
		node = yamlString(value)
		if list {
			node = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle, Content: []*yaml.Node{node}}
		}
		mapping.Content = append(mapping.Content, yamlString(key), node)
		return true
	}
	if list && node.Kind == yaml.SequenceNode {
		if len(node.Content) == 1 && node.Content[0].Kind == yaml.ScalarNode && node.Content[0].Value == value {
			return false
		}
		item := yamlString(value)
		if len(node.Content) > 0 && node.Content[0].Kind == yaml.ScalarNode {
			item = node.Content[0]
			item.Value, item.Tag = value, "!!str"
		}
		node.Content = []*yaml.Node{item}
		return true
	}
	if node.Kind == yaml.ScalarNode {
		if node.Value == value && node.Tag == "!!str" {
			return false
		}
		node.Value, node.Tag = value, "!!str"
		return true
	}
	newNode := yamlString(value)
	newNode.HeadComment, newNode.LineComment, newNode.FootComment = node.HeadComment, node.LineComment, node.FootComment
	mapping.Content[index+1] = newNode
	return true
}

func removeYAMLKeys(mapping *yaml.Node, key string, fold bool) bool {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return false
	}
	changed := false
	for i := 0; i+1 < len(mapping.Content); {
		name := mapping.Content[i].Value
		if name == key || (fold && strings.EqualFold(name, key)) {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			changed = true
		} else {
			i += 2
		}
	}
	return changed
}

// HTTP header names are case-insensitive; leave just the first spelling so
// stale variants cannot conflict with the newly written override.
func deduplicateYAMLHeader(headers *yaml.Node, key string) bool {
	found, changed := false, false
	for i := 0; i+1 < len(headers.Content); {
		if strings.EqualFold(headers.Content[i].Value, key) {
			if found {
				headers.Content = append(headers.Content[:i], headers.Content[i+2:]...)
				changed = true
				continue
			}
			found = true
		}
		i += 2
	}
	return changed
}

func moveYAMLHeaderFirst(headers *yaml.Node, key string) bool {
	_, index := yamlField(headers, key, true)
	if index <= 0 {
		return false
	}
	pair := append([]*yaml.Node(nil), headers.Content[index:index+2]...)
	copy(headers.Content[2:index+2], headers.Content[:index])
	copy(headers.Content[:2], pair)
	return true
}

func setClashHeader(parent *yaml.Node, key, value string, list bool) (bool, error) {
	headers, err := yamlEnsureMapping(parent, "headers")
	if err != nil {
		return false, err
	}
	changed := deduplicateYAMLHeader(headers, key)
	return setYAMLString(headers, key, value, true, list) || changed, nil
}

func applyClashOverride(node *yaml.Node, proxyType string, override *option.OverrideDialerOptions) (bool, error) {
	changed := false
	if proxyType == "http" {
		if override.Host != nil {
			c, err := setClashHeader(node, "Host", *override.Host, false)
			if err != nil {
				return false, err
			}
			changed = changed || c
			if c {
				headers, _ := yamlField(node, "headers", false)
				changed = moveYAMLHeaderFirst(headers, "With-At") || changed
			}
		}
		if override.Path != nil {
			if *override.Path == "" {
				changed = removeYAMLKeys(node, "path", false) || changed
			} else {
				changed = setYAMLString(node, "path", *override.Path, false, false) || changed
			}
		}
		if override.WithAt != nil {
			headers, _ := yamlField(node, "headers", false)
			if *override.WithAt == "" {
				changed = removeYAMLKeys(headers, "With-At", true) || changed
			} else {
				var err error
				headers, err = yamlEnsureMapping(node, "headers")
				if err != nil {
					return false, err
				}
				changed = deduplicateYAMLHeader(headers, "With-At") || changed
				changed = setYAMLString(headers, "With-At", *override.WithAt, true, false) || changed
				changed = moveYAMLHeaderFirst(headers, "With-At") || changed
			}
		}
		return changed, nil
	}
	if override.Host == nil || (proxyType != "vmess" && proxyType != "vless" && proxyType != "trojan") {
		return false, nil
	}
	network, _ := yamlField(node, "network", false)
	if network == nil || network.Kind != yaml.ScalarNode {
		return false, nil
	}
	var optionsKey string
	switch network.Value {
	case "http":
		optionsKey = "http-opts"
	case "h2":
		optionsKey = "h2-opts"
	case "ws":
		optionsKey = "ws-opts"
	case "xhttp":
		optionsKey = "xhttp-opts"
	default:
		return false, nil
	}
	opts, err := yamlEnsureMapping(node, optionsKey)
	if err != nil {
		return false, err
	}
	switch network.Value {
	case "http", "ws":
		return setClashHeader(opts, "Host", *override.Host, network.Value == "http")
	case "h2":
		return setYAMLString(opts, "host", *override.Host, false, true), nil
	case "xhttp":
		// Download settings may inherit the upload Host. An override applies
		// only to upload, so materialize the previous inherited download Host
		// if changing upload would otherwise alter it on the next parse.
		beforeHost, hasDownload, err := clashDownloadHost(node)
		if err != nil {
			return false, err
		}
		changed = setYAMLString(opts, "host", *override.Host, false, false)
		headers, _ := yamlField(opts, "headers", false)
		changed = removeYAMLKeys(headers, "Host", true) || changed
		if hasDownload && changed {
			afterHost, _, err := clashDownloadHost(node)
			if err != nil {
				return false, err
			}
			if !reflect.DeepEqual(beforeHost, afterHost) {
				download, err := yamlEnsureMapping(opts, "download-settings")
				if err != nil {
					return false, err
				}
				changed = setYAMLString(download, "host", strings.Join(beforeHost, ","), false, false) || changed
			}
		}
		return changed, nil
	}
	return false, nil
}

// clashDownloadHost obtains the converted download Host before and after a
// YAML edit, accounting for inheritance and an explicit download header.
func clashDownloadHost(node *yaml.Node) ([]string, bool, error) {
	var proxy parser.ClashProxy
	if err := node.Decode(&proxy); err != nil {
		return nil, false, err
	}
	if proxy.Options == nil {
		return nil, false, nil
	}
	var transport *option.V2RayTransportOptions
	switch options := proxy.Options.Build().(type) {
	case *option.VMessOutboundOptions:
		transport = options.Transport
	case *option.VLESSOutboundOptions:
		transport = options.Transport
	case *option.TrojanOutboundOptions:
		transport = options.Transport
	}
	if transport == nil {
		return nil, false, nil
	}
	xhttp, ok := transport.Extra.(*option.V2RayXHTTPOptions)
	if !ok || xhttp == nil || xhttp.DownloadSettings == nil {
		return nil, false, nil
	}
	return append([]string(nil), xhttp.DownloadSettings.Host...), true, nil
}
