package parser

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sagernet/sing-box/adapter/outbound"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/protocol/cns"
	"github.com/sagernet/sing/service"
	"github.com/stretchr/testify/require"
)

func TestPathOverrideSubscription(t *testing.T) {
	registry := outbound.NewRegistry()
	outbound.Register[option.HTTPOutboundOptions](registry, C.TypeHTTP, nil)
	outbound.Register[option.VLESSOutboundOptions](registry, C.TypeVLESS, nil)
	cns.RegisterOutbound(registry)
	ctx := service.ContextWith[option.OutboundOptionsRegistry](context.Background(), registry)
	content := `{"outbounds":[
		{"type":"http","tag":"one","server":"180.101.50.208","server_port":443,"path":"@gw.alicdn.com","del_host":true,"headers":{"X-Test":"kept"}},
		{"type":"http","tag":"two","server":"220.181.7.1","server_port":443},
		{"type":"vless","tag":"ws","server":"example.com","server_port":443,"uuid":"bf000d23-0752-40b4-affe-68f7707a9661","transport":{"type":"ws","path":"/ws"}},
		{"type":"cns","tag":"cns","server":"127.0.0.1","server_port":5002}
	]}`
	for _, tt := range []struct {
		name, config, first, second string
	}{
		{"unset", `{}`, "@gw.alicdn.com", ""},
		{"set", `{"path":"@new.example"}`, "@new.example", "@new.example"},
		{"clear", `{"path":""}`, "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var override option.OverrideTransportOptions
			require.NoError(t, json.Unmarshal([]byte(tt.config), &override))
			nodes, _, err := ParseSubscription(ctx, content, nil, nil, nil, &override, "provider")
			require.NoError(t, err)
			require.Len(t, nodes, 4)
			one := nodes[0].Options.(*option.HTTPOutboundOptions)
			two := nodes[1].Options.(*option.HTTPOutboundOptions)
			require.Equal(t, tt.first, one.Path)
			require.Equal(t, tt.second, two.Path)
			require.True(t, one.DelHost)
			require.Empty(t, one.Headers.Build().Get("Host"))
			require.Equal(t, "kept", one.Headers.Build().Get("X-Test"))
			require.Equal(t, "/ws", nodes[2].Options.(*option.VLESSOutboundOptions).Transport.WebsocketOptions.Path)
			require.Empty(t, nodes[3].Options.(*option.CNSOutboundOptions).Headers)
		})
	}
}
