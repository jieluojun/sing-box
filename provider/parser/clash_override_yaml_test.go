package parser

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	"github.com/stretchr/testify/require"
)

func TestClashHTTPTransportHostHeaderUsesAuthority(t *testing.T) {
	const source = `proxies:
  - {name: vmess, type: vmess, server: example.com, port: 443, uuid: bf000d23-0752-40b4-affe-68f7707a9661, cipher: auto, tls: true, network: http, http-opts: {method: GET, path: [/keep], headers: {hOsT: [old.example], X-Test: [keep]}}}
  - {name: trojan, type: trojan, server: example.com, port: 443, password: test, network: http, http-opts: {method: POST, path: [/trojan], headers: {Host: [old.example]}}}
`
	nodes, _, err := ParseSubscription(context.Background(), source, nil, nil, nil, nil, "local")
	require.NoError(t, err)
	require.Len(t, nodes, 2)
	vm := nodes[0].Options.(*option.VMessOutboundOptions).Transport.HTTPOptions
	require.Equal(t, []string{"old.example"}, []string(vm.Host))
	require.Equal(t, "/keep", vm.Path)
	require.Equal(t, "GET", vm.Method)
	require.Equal(t, "keep", vm.Headers.Build().Get("X-Test"))
	require.Empty(t, vm.Headers.Build().Get("Host"))
	trojan := nodes[1].Options.(*option.TrojanOutboundOptions).Transport.HTTPOptions
	require.Equal(t, []string{"old.example"}, []string(trojan.Host))
	require.Equal(t, "/trojan", trojan.Path)
	require.Equal(t, "POST", trojan.Method)
	updated, _, err := ParseSubscription(context.Background(), source, &option.OverrideDialerOptions{Host: common.Ptr("new.example")}, nil, nil, nil, "local")
	require.NoError(t, err)
	require.Equal(t, []string{"new.example"}, []string(updated[0].Options.(*option.VMessOutboundOptions).Transport.HTTPOptions.Host))
}

func TestClashHTTPConnectPathAndWithAt(t *testing.T) {
	const source = `proxies:
  - {name: connect, type: http, server: example.com, port: 8080, path: "@path.example", headers: {With-At: at.example, X-Test: keep}}
`
	nodes, _, err := ParseSubscription(context.Background(), source, nil, nil, nil, nil, "local")
	require.NoError(t, err)
	options := nodes[0].Options.(*option.HTTPOutboundOptions)
	require.Equal(t, "@path.example", options.Path)
	require.Equal(t, "at.example", options.Headers.Build().Get("With-At"))
	require.Equal(t, "keep", options.Headers.Build().Get("X-Test"))
	override := &option.OverrideDialerOptions{Host: common.Ptr("new.example"), Path: common.Ptr("@new-path.example"), WithAt: common.Ptr("new-at.example")}
	updated, _, err := ParseSubscription(context.Background(), source, override, nil, nil, nil, "remote")
	require.NoError(t, err)
	actual := updated[0].Options.(*option.HTTPOutboundOptions)
	require.Equal(t, "new.example", actual.Headers.Build().Get("Host"))
	require.Equal(t, "@new-path.example", actual.Path)
	require.Equal(t, "new-at.example", actual.Headers.Build().Get("With-At"))
	require.Equal(t, "keep", actual.Headers.Build().Get("X-Test"))
}
