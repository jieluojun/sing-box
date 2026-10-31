package parser

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sagernet/sing-box/adapter/outbound"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/protocol/cns"
	"github.com/sagernet/sing/common/json/badoption"
	"github.com/sagernet/sing/service"
	"github.com/stretchr/testify/require"
)

func TestWithAtOverrideSubscription(t *testing.T) {
	registry := outbound.NewRegistry()
	outbound.Register[option.HTTPOutboundOptions](registry, C.TypeHTTP, nil)
	cns.RegisterOutbound(registry)
	ctx := service.ContextWith[option.OutboundOptionsRegistry](context.Background(), registry)
	content := `{"outbounds":[
		{"tag":"one","type":"http","server":"180.101.50.208","server_port":443,"headers":{"With-At":"old.example","Host":"153.3.236.22:443","X-T5-Auth":"683556433"}},
		{"tag":"two","type":"http","server":"220.181.7.1","server_port":443},
		{"tag":"cns","type":"cns","server":"127.0.0.1","server_port":5002,"headers":{"With-At":"untouched"}}
	]}`
	for _, tt := range []struct {
		name, config, first, second string
	}{
		{"unset", `{}`, "old.example", ""},
		{"set", `{"with_at":"gw.alicdn.com"}`, "gw.alicdn.com", "gw.alicdn.com"},
		{"clear", `{"with_at":""}`, "", ""},
		{"alias", `{"With-At":"gw.alicdn.com"}`, "gw.alicdn.com", "gw.alicdn.com"},
		{"alias clear", `{"With-At":""}`, "", ""},
		{"both canonical wins", `{"with_at":"first.example","With-At":"second.example"}`, "first.example", "first.example"},
		{"both reverse order", `{"With-At":"second.example","with_at":"first.example"}`, "first.example", "first.example"},
		{"empty canonical wins", `{"With-At":"second.example","with_at":""}`, "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var override option.OverrideTransportOptions
			require.NoError(t, json.Unmarshal([]byte(tt.config), &override))
			nodes, _, err := ParseSubscription(ctx, content, nil, nil, nil, &override, "provider")
			require.NoError(t, err)
			require.Len(t, nodes, 3)
			one := nodes[0].Options.(*option.HTTPOutboundOptions)
			two := nodes[1].Options.(*option.HTTPOutboundOptions)
			require.Equal(t, tt.first, one.Headers.Build().Get("With-At"))
			require.Equal(t, tt.second, two.Headers.Build().Get("With-At"))
			require.Equal(t, "153.3.236.22:443", one.Headers.Build().Get("Host"))
			require.Equal(t, "683556433", one.Headers.Build().Get("X-T5-Auth"))
			require.Equal(t, "untouched", nodes[2].Options.(*option.CNSOutboundOptions).Headers.Build().Get("With-At"))
		})
	}
}

func TestWithAtOverrideCaseInsensitiveAndIndependent(t *testing.T) {
	headers := badoption.HTTPHeader{
		"WITH-AT": {"old1"}, "with-at": {"old2"}, "Host": {"preserved"},
	}
	header := overrideWithAtHeader(headers, "gw.alicdn.com")
	require.NotContains(t, header, "WITH-AT")
	require.NotContains(t, header, "with-at")
	require.Equal(t, "gw.alicdn.com", header.Build().Get("With-At"))
	require.Equal(t, "preserved", header.Build().Get("Host"))
	header = overrideWithAtHeader(header, "")
	require.NotContains(t, header, "With-At")
	require.Nil(t, overrideWithAtHeader(nil, ""))
}

func TestWithAtOverrideCanClearExistingPath(t *testing.T) {
	original := "@gw.alicdn.com"
	path := ""
	withAt := "new.example"
	nodes := []option.Outbound{{Type: C.TypeHTTP, Options: &option.HTTPOutboundOptions{Path: original}}}
	result := overrideOutbounds(nodes, nil, nil, nil, &option.OverrideTransportOptions{Path: &path, WithAt: &withAt}, nil, "")
	opts := result[0].Options.(*option.HTTPOutboundOptions)
	require.Empty(t, opts.Path)
	require.Equal(t, "new.example", opts.Headers.Build().Get("With-At"))
}
