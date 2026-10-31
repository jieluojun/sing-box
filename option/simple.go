package option

import (
	"context"
	"reflect"
	"slices"
	"strings"

	"github.com/sagernet/sing-box/schema"
	"github.com/sagernet/sing/common/auth"
	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/common/json/badjson"
	"github.com/sagernet/sing/common/json/badoption"
)

type SocksInboundOptions struct {
	ListenOptions
	Users          []auth.User           `json:"users,omitempty"`
	DomainResolver *DomainResolveOptions `json:"domain_resolver,omitempty"`
}

type HTTPMixedInboundOptions struct {
	ListenOptions
	Users          []auth.User           `json:"users,omitempty"`
	DomainResolver *DomainResolveOptions `json:"domain_resolver,omitempty"`
	SetSystemProxy bool                  `json:"set_system_proxy,omitempty"`
	InboundTLSOptionsContainer
}

type _HTTPInboundOptions struct {
	ListenOptions
	Users          []auth.User             `json:"users,omitempty"`
	DomainResolver *DomainResolveOptions   `json:"domain_resolver,omitempty"`
	SetSystemProxy bool                    `json:"set_system_proxy,omitempty"`
	Version        badoption.Listable[int] `json:"version,omitempty" enum:"1,2,3"`
	InboundTLSOptionsContainer
	HTTP2Options HTTP2Options `json:"-"`
	HTTP3Options QUICOptions  `json:"-"`
}

type HTTPInboundOptions _HTTPInboundOptions

func (o HTTPInboundOptions) Versions() []int {
	if len(o.Version) > 0 {
		return o.Version
	}
	return []int{1, 2}
}

func (o HTTPInboundOptions) MarshalJSON() ([]byte, error) {
	return badjson.MarshallObjects(_HTTPInboundOptions(o), httpVersionsVariant(o.Versions(), o.HTTP2Options, o.HTTP3Options))
}

func (o *HTTPInboundOptions) UnmarshalJSONContext(ctx context.Context, content []byte) error {
	err := json.UnmarshalContext(ctx, content, (*_HTTPInboundOptions)(o))
	if err != nil {
		return err
	}
	return unmarshalHTTPVersionsOptions(ctx, content, (*_HTTPInboundOptions)(o), o.Versions(), &o.HTTP2Options, &o.HTTP3Options)
}

func (o HTTPInboundOptions) DescribeSchema(builder schema.Builder) (*schema.Node, error) {
	node := schema.StrictObject()
	err := builder.FlattenStruct(node, reflect.TypeFor[HTTPInboundOptions]())
	if err != nil {
		return nil, err
	}
	err = builder.FlattenStruct(node, reflect.TypeFor[QUICOptions]())
	if err != nil {
		return nil, err
	}
	return node, nil
}

type SOCKSOutboundOptions struct {
	DialerOptions
	ServerOptions
	Version             string                `json:"version,omitempty" enum:"4,4a,5"`
	Username            string                `json:"username,omitempty"`
	Password            string                `json:"password,omitempty"`
	Network             NetworkList           `json:"network,omitempty"`
	UDPOverTCP          *UDPOverTCPOptions    `json:"udp_over_tcp,omitempty"`
	InnerDomainResolver *DomainResolveOptions `json:"inner_domain_resolver,omitempty"`
}

func (o *SOCKSOutboundOptions) TakeInnerDomainResolverOptions() *DomainResolveOptions {
	if o.Version != "4" {
		return nil
	}
	return o.InnerDomainResolver
}

type _HTTPOutboundOptions struct {
	DialerOptions
	ServerOptions
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	OutboundTLSOptionsContainer
	Path                   string               `json:"path,omitempty"`
	DelHost                bool                 `json:"del_host,omitempty"`
	Headers                badoption.HTTPHeader `json:"headers,omitempty"`
	Network                NetworkList          `json:"network,omitempty"`
	Version                int                  `json:"version,omitempty" enum:"0,1,2,3"`
	DisableVersionFallback bool                 `json:"disable_version_fallback,omitempty"`
	HTTP2Options           HTTP2Options         `json:"-"`
	HTTP3Options           QUICOptions          `json:"-"`
}

type HTTPOutboundOptions _HTTPOutboundOptions

// orderedHTTPOutboundHeaders keeps the local CONNECT option first in saved
// subscription JSON. JSON object order does not affect HTTP request behavior.
type orderedHTTPOutboundHeaders badoption.HTTPHeader

func (h orderedHTTPOutboundHeaders) MarshalJSONContext(ctx context.Context) ([]byte, error) {
	keys := make([]string, 0, len(h))
	for key := range h {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	var result badjson.JSONObject
	for _, key := range keys {
		if strings.EqualFold(key, "With-At") {
			result.Put(key, h[key])
		}
	}
	for _, key := range keys {
		if !strings.EqualFold(key, "With-At") {
			result.Put(key, h[key])
		}
	}
	return result.MarshalJSONContext(ctx)
}

func (o HTTPOutboundOptions) MarshalJSON() ([]byte, error) {
	options := struct {
		_HTTPOutboundOptions
		Headers orderedHTTPOutboundHeaders `json:"headers,omitempty"`
	}{_HTTPOutboundOptions: _HTTPOutboundOptions(o), Headers: orderedHTTPOutboundHeaders(o.Headers)}
	return badjson.MarshallObjects(options, httpVersionVariant(o.Version, o.HTTP2Options, o.HTTP3Options))
}

func (o *HTTPOutboundOptions) UnmarshalJSONContext(ctx context.Context, content []byte) error {
	err := json.UnmarshalContext(ctx, content, (*_HTTPOutboundOptions)(o))
	if err != nil {
		return err
	}
	return unmarshalHTTPVersionOptions(ctx, content, (*_HTTPOutboundOptions)(o), o.Version, &o.HTTP2Options, &o.HTTP3Options)
}

func (o HTTPOutboundOptions) DescribeSchema(builder schema.Builder) (*schema.Node, error) {
	node := schema.StrictObject()
	err := builder.FlattenStruct(node, reflect.TypeFor[HTTPOutboundOptions]())
	if err != nil {
		return nil, err
	}
	err = builder.FlattenStruct(node, reflect.TypeFor[QUICOptions]())
	if err != nil {
		return nil, err
	}
	return node, nil
}
