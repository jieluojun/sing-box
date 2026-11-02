package parser

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sagernet/sing-box/adapter/outbound"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/service"
	"github.com/stretchr/testify/require"
)

func TestOverrideDialerFromLocalAndRemoteConfig(t *testing.T) {
	registry := outbound.NewRegistry()
	outbound.Register[option.HTTPOutboundOptions](registry, C.TypeHTTP, nil)
	outbound.Register[option.VLESSOutboundOptions](registry, C.TypeVLESS, nil)
	ctx := service.ContextWith[option.OutboundOptionsRegistry](context.Background(), registry)
	content := `{"outbounds":[
		{"type":"http","tag":"http","server":"example.com","server_port":443,"headers":{"Host":"old.example","With-At":"old.example","X-Test":"kept"}},
		{"type":"vless","tag":"ws","server":"example.com","server_port":443,"uuid":"bf000d23-0752-40b4-affe-68f7707a9661","transport":{"type":"ws","path":"/ws","headers":{"Host":"old.example"}}}
	]}`
	for _, tt := range []struct {
		name  string
		parse func() (*option.OverrideDialerOptions, *option.OverrideTransportOptions)
	}{
		{"local", func() (*option.OverrideDialerOptions, *option.OverrideTransportOptions) {
			var provider option.ProviderLocalOptions
			require.NoError(t, json.Unmarshal([]byte(`{"path":"sub.json","override_dialer":{"host":"v.qq.com","path":"@example.com","with_at":"gw.example","detour":"direct"},"override_transport":{"xhttp_download_enabled":true}}`), &provider))
			return provider.OverrideDialer, provider.OverrideTransport
		}},
		{"remote", func() (*option.OverrideDialerOptions, *option.OverrideTransportOptions) {
			var provider option.ProviderRemoteOptions
			require.NoError(t, json.Unmarshal([]byte(`{"url":"https://example.com/sub","override_dialer":{"host":"v.qq.com","path":"@example.com","with_at":"gw.example","detour":"direct"},"override_transport":{"xhttp_download_enabled":true}}`), &provider))
			return provider.OverrideDialer, provider.OverrideTransport
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dialer, transport := tt.parse()
			nodes, _, err := ParseSubscription(ctx, content, dialer, nil, nil, transport, "provider")
			require.NoError(t, err)
			require.Len(t, nodes, 2)
			http := nodes[0].Options.(*option.HTTPOutboundOptions)
			require.Equal(t, "v.qq.com", http.Headers.Build().Get("Host"))
			require.Equal(t, "gw.example", http.Headers.Build().Get("With-At"))
			require.Equal(t, "kept", http.Headers.Build().Get("X-Test"))
			require.Equal(t, "@example.com", http.Path)
			require.Equal(t, "direct", http.Detour)
			ws := nodes[1].Options.(*option.VLESSOutboundOptions)
			require.Equal(t, "/ws", ws.Transport.WebsocketOptions.Path)
			require.Equal(t, "v.qq.com", ws.Transport.WebsocketOptions.Headers.Build().Get("Host"))
			require.Equal(t, "direct", ws.Detour)
		})
	}
}
