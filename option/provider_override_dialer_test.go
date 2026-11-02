package option

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/sagernet/sing-box/schema"
	singjson "github.com/sagernet/sing/common/json"
	"github.com/stretchr/testify/require"
)

func TestProviderOverrideDialerFields(t *testing.T) {
	for _, tt := range []struct {
		name string
		data string
		out  any
	}{
		{"local", `{"path":"sub.json","override_dialer":{"host":"v.qq.com","path":"@example.com","with_at":"gw.example","detour":"direct"},"override_transport":{"xhttp_download_enabled":true,"xhttp_download_detour":"other"}}`, &ProviderLocalOptions{}},
		{"remote", `{"url":"https://example.com/sub","override_dialer":{"host":"v.qq.com","path":"@example.com","with_at":"gw.example","detour":"direct"},"override_transport":{"xhttp_download_enabled":true,"xhttp_download_detour":"other"}}`, &ProviderRemoteOptions{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, singjson.UnmarshalContextDisallowUnknownFields(context.Background(), []byte(tt.data), tt.out))
			encoded, err := json.Marshal(tt.out)
			require.NoError(t, err)
			var got map[string]any
			require.NoError(t, json.Unmarshal(encoded, &got))
			require.Equal(t, map[string]any{"host": "v.qq.com", "path": "@example.com", "with_at": "gw.example", "detour": "direct"}, got["override_dialer"])
			require.Equal(t, map[string]any{"xhttp_download_enabled": true, "xhttp_download_detour": "other"}, got["override_transport"])
		})
	}
}

func TestOldTransportOverridesRejected(t *testing.T) {
	for _, field := range []string{"host", "path", "with_at", "With-At"} {
		t.Run(field, func(t *testing.T) {
			var options OverrideTransportOptions
			err := singjson.UnmarshalContextDisallowUnknownFields(context.Background(), []byte(`{"`+field+`":"value"}`), &options)
			require.Error(t, err)
			for _, provider := range []any{&ProviderLocalOptions{}, &ProviderRemoteOptions{}} {
				err = singjson.UnmarshalContextDisallowUnknownFields(context.Background(), []byte(`{"override_transport":{"`+field+`":"value"}}`), provider)
				require.Error(t, err)
			}
		})
	}
}

func TestProviderOverrideSchemasMatchDocumentation(t *testing.T) {
	document, err := os.ReadFile("../docs/schema.json")
	require.NoError(t, err)
	var saved struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	require.NoError(t, json.Unmarshal(document, &saved))
	for _, tt := range []struct {
		name   string
		typeOf reflect.Type
	}{
		{"OverrideDialerOptions", reflect.TypeFor[OverrideDialerOptions]()},
		{"OverrideTransportOptions", reflect.TypeFor[OverrideTransportOptions]()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			generated, err := schema.Generate(context.Background(), tt.typeOf)
			require.NoError(t, err)
			var result struct {
				Defs map[string]json.RawMessage `json:"$defs"`
			}
			require.NoError(t, json.Unmarshal(generated, &result))
			require.Contains(t, result.Defs, tt.name)
			require.Contains(t, saved.Defs, tt.name)
			require.JSONEq(t, string(saved.Defs[tt.name]), string(result.Defs[tt.name]))
		})
	}
}
