package parser

import (
	"context"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/adapter/outbound"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/service"
	"github.com/stretchr/testify/require"
)

func TestWithAtOverrideSavedFirstAndRoundTrips(t *testing.T) {
	registry := outbound.NewRegistry()
	outbound.Register[option.HTTPOutboundOptions](registry, C.TypeHTTP, nil)
	ctx := service.ContextWith[option.OutboundOptionsRegistry](context.Background(), registry)
	content := `{"outbounds":[{"type":"http","tag":"node","server":"180.101.50.208","server_port":443,"headers":{"Host":"153.3.236.22:443","X-T5-Auth":"683556433","A-Header":["one","two"]}}]}`
	for _, override := range []*option.OverrideTransportOptions{
		{WithAt: common.Ptr("gw.alicdn.com")},
	} {
		nodes, _, err := ParseSubscription(ctx, content, nil, nil, nil, override, "provider")
		require.NoError(t, err)
		encoded, err := json.Marshal(option.Options{Outbounds: nodes})
		require.NoError(t, err)
		require.Contains(t, string(encoded), `"headers":{"With-At":"gw.alicdn.com",`)
		require.True(t, strings.Index(string(encoded), `"With-At"`) < strings.Index(string(encoded), `"Host"`))

		reloaded, _, err := ParseSubscription(ctx, string(encoded), nil, nil, nil, nil, "provider")
		require.NoError(t, err)
		headers := reloaded[0].Options.(*option.HTTPOutboundOptions).Headers.Build()
		require.Equal(t, "gw.alicdn.com", headers.Get("With-At"))
		require.Equal(t, "153.3.236.22:443", headers.Get("Host"))
		require.Equal(t, "683556433", headers.Get("X-T5-Auth"))
		require.Equal(t, []string{"one", "two"}, headers.Values("A-Header"))
	}
}
