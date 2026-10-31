package parser

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/adapter/outbound"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/protocol/cns"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/json/badoption"
	"github.com/sagernet/sing/service"
	"github.com/stretchr/testify/require"
)

func TestOverrideHostTransports(t *testing.T) {
	makeHeaders := func() badoption.HTTPHeader {
		return badoption.HTTPHeader{"hOsT": {"old.example"}, "X-Test": {"kept"}}
	}
	transports := []*option.V2RayTransportOptions{
		{Type: C.V2RayTransportTypeWebsocket, WebsocketOptions: option.V2RayWebsocketOptions{Headers: makeHeaders()}},
		{Type: C.V2RayTransportTypeHTTP, HTTPOptions: option.V2RayHTTPOptions{Host: []string{"old.example"}, Headers: makeHeaders()}},
		{Type: C.V2RayTransportTypeHTTPUpgrade, HTTPUpgradeOptions: option.V2RayHTTPUpgradeOptions{Host: "old.example", Headers: makeHeaders()}},
		{Type: C.V2RayTransportTypeXHTTP, Extra: &option.V2RayXHTTPOptions{Host: []string{"old.example"}, Headers: makeHeaders(), DownloadSettings: &option.V2RayXHTTPDownloadOptions{V2RayXHTTPOptions: option.V2RayXHTTPOptions{Host: []string{"download.example"}}}}},
	}
	override := &option.OverrideTransportOptions{Host: common.Ptr("v.qq.com")}
	for _, transport := range transports {
		t.Run(transport.Type, func(t *testing.T) {
			result := overrideTransportOption(transport, nil, override)
			var headers badoption.HTTPHeader
			switch result.Type {
			case C.V2RayTransportTypeWebsocket:
				headers = result.WebsocketOptions.Headers
				require.Equal(t, []string{"v.qq.com"}, []string(headers["Host"]))
			case C.V2RayTransportTypeHTTP:
				headers = result.HTTPOptions.Headers
				require.Equal(t, []string{"v.qq.com"}, []string(result.HTTPOptions.Host))
			case C.V2RayTransportTypeHTTPUpgrade:
				headers = result.HTTPUpgradeOptions.Headers
				require.Equal(t, "v.qq.com", result.HTTPUpgradeOptions.Host)
			case C.V2RayTransportTypeXHTTP:
				x := result.Extra.(*option.V2RayXHTTPOptions)
				headers = x.Headers
				require.Equal(t, []string{"v.qq.com"}, []string(x.Host))
				require.Equal(t, []string{"download.example"}, []string(x.DownloadSettings.Host))
			}
			require.NotContains(t, headers, "hOsT")
			require.Equal(t, []string{"kept"}, []string(headers["X-Test"]))
		})
	}
	require.Nil(t, overrideTransportOption(nil, nil, override))
	grpc := &option.V2RayTransportOptions{Type: C.V2RayTransportTypeGRPC, GRPCOptions: option.V2RayGRPCOptions{ServiceName: "service"}}
	require.Equal(t, "service", overrideTransportOption(grpc, nil, override).GRPCOptions.ServiceName)
}

func TestOverrideHostOutboundsAndTLSIndependent(t *testing.T) {
	outbounds := []option.Outbound{
		{Type: C.TypeCNS, Options: &option.CNSOutboundOptions{Headers: badoption.HTTPHeader{"host": {"old"}, "X-Test": {"kept"}}}},
		{Type: C.TypeHTTP, Options: &option.HTTPOutboundOptions{}},
		{Type: C.TypeVLESS, Options: &option.VLESSOutboundOptions{OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{TLS: &option.OutboundTLSOptions{Enabled: true, ServerName: "sni.example"}}, Transport: &option.V2RayTransportOptions{Type: C.V2RayTransportTypeWebsocket}}},
	}
	detour := "direct"
	got := overrideOutbounds(outbounds, &option.OverrideDialerOptions{Detour: &detour}, nil, nil, &option.OverrideTransportOptions{Host: common.Ptr("v.qq.com")}, nil, "")
	cns := got[0].Options.(*option.CNSOutboundOptions)
	require.Equal(t, "direct", cns.Detour)
	require.Equal(t, "v.qq.com", cns.Headers.Build().Get("Host"))
	require.Equal(t, "kept", cns.Headers.Build().Get("X-Test"))
	require.NotContains(t, cns.Headers, "host")
	require.Equal(t, "v.qq.com", got[1].Options.(*option.HTTPOutboundOptions).Headers.Build().Get("Host"))
	vless := got[2].Options.(*option.VLESSOutboundOptions)
	require.Equal(t, "sni.example", vless.TLS.ServerName)
	require.Equal(t, "v.qq.com", vless.Transport.WebsocketOptions.Headers.Build().Get("Host"))
}

func TestOverrideHostClashSubscription(t *testing.T) {
	content := `proxies:
  - {name: ws, type: vless, server: example.com, port: 443, uuid: bf000d23-0752-40b4-affe-68f7707a9661, tls: true, servername: sni.example, network: ws, ws-opts: {headers: {Host: old.example}}}
  - {name: http, type: vmess, server: example.com, port: 443, uuid: bf000d23-0752-40b4-affe-68f7707a9661, cipher: auto, network: http, http-opts: {headers: {Host: [old.example]}}}
`
	outbounds, _, err := ParseSubscription(context.Background(), content, nil, nil, nil, &option.OverrideTransportOptions{Host: common.Ptr("v.qq.com")}, "test")
	require.NoError(t, err)
	require.Len(t, outbounds, 2)
	ws := outbounds[0].Options.(*option.VLESSOutboundOptions)
	require.Equal(t, "sni.example", ws.TLS.ServerName)
	require.Equal(t, "v.qq.com", ws.Transport.WebsocketOptions.Headers.Build().Get("Host"))
	http := outbounds[1].Options.(*option.VMessOutboundOptions)
	require.Equal(t, []string{"v.qq.com"}, []string(http.Transport.HTTPOptions.Host))
}

func TestOverrideHostNotSetPreservesValues(t *testing.T) {
	transport := &option.V2RayTransportOptions{Type: C.V2RayTransportTypeWebsocket, WebsocketOptions: option.V2RayWebsocketOptions{Headers: badoption.HTTPHeader{"host": {"old"}}}}
	overrideTransportOption(transport, nil, &option.OverrideTransportOptions{})
	require.Equal(t, []string{"old"}, []string(transport.WebsocketOptions.Headers["host"]))
}

func TestOverrideHostBoxCNSSubscription(t *testing.T) {
	registry := outbound.NewRegistry()
	cns.RegisterOutbound(registry)
	ctx := service.ContextWith[option.OutboundOptionsRegistry](context.Background(), registry)
	content := `{"outbounds":[{"type":"cns","tag":"node","server":"127.0.0.1","server_port":5002,"key":"Meng","headers":{"host":"old.example","X-Test":"kept"}}]}`
	nodes, _, err := ParseSubscription(ctx, content, nil, nil, nil, &option.OverrideTransportOptions{Host: common.Ptr("v.qq.com")}, "provider")
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	node := nodes[0].Options.(*option.CNSOutboundOptions)
	require.Equal(t, "v.qq.com", node.Headers.Build().Get("Host"))
	require.Equal(t, "kept", node.Headers.Build().Get("X-Test"))
	require.Equal(t, "Meng", node.Key)
}
