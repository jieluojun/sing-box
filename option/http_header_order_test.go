package option

import (
	"context"
	"encoding/json"
	"testing"

	singjson "github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/common/json/badjson"
	"github.com/sagernet/sing/common/json/badoption"
	"github.com/stretchr/testify/require"
)

func TestHTTPOutboundHeaderSerialization(t *testing.T) {
	for _, test := range []struct {
		name    string
		options HTTPOutboundOptions
	}{
		{"no headers", HTTPOutboundOptions{}},
		{"empty headers", HTTPOutboundOptions{Headers: badoption.HTTPHeader{}}},
		{"ordinary headers", HTTPOutboundOptions{Headers: badoption.HTTPHeader{"Host": {"example.com"}, "X-Test": {"one", "two"}}}},
		{"lowercase with-at", HTTPOutboundOptions{Headers: badoption.HTTPHeader{"with-at": {"example.com"}, "A-Test": {"first"}}}},
		{"http2 options", HTTPOutboundOptions{Version: 2, HTTP2Options: HTTP2Options{MaxConcurrentStreams: 19}, Headers: badoption.HTTPHeader{"With-At": {"example.com"}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			before, err := badjson.MarshallObjects(_HTTPOutboundOptions(test.options), httpVersionVariant(test.options.Version, test.options.HTTP2Options, test.options.HTTP3Options))
			require.NoError(t, err)
			after, err := test.options.MarshalJSON()
			require.NoError(t, err)
			var beforeValue, afterValue any
			require.NoError(t, json.Unmarshal(before, &beforeValue))
			require.NoError(t, json.Unmarshal(after, &afterValue))
			require.Equal(t, beforeValue, afterValue)
			if test.name == "lowercase with-at" {
				require.Contains(t, string(after), `"with-at":"example.com","A-Test":"first"`)
			}
		})
	}
}

func TestOverrideTransportWithAtKey(t *testing.T) {
	var options OverrideTransportOptions
	err := singjson.UnmarshalContextDisallowUnknownFields(context.Background(), []byte(`{"with_at":"gw.alicdn.com"}`), &options)
	require.NoError(t, err)
	require.Equal(t, "gw.alicdn.com", *options.WithAt)
	err = singjson.UnmarshalContextDisallowUnknownFields(context.Background(), []byte(`{"With-At":"gw.alicdn.com"}`), &options)
	require.Error(t, err)
}
