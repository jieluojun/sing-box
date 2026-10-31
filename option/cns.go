package option

import "github.com/sagernet/sing/common/json/badoption"

type CNSInboundOptions struct {
	ListenOptions
	Key      string `json:"key,omitempty"`
	Password string `json:"password,omitempty"`
	Flag     string `json:"flag,omitempty"`
}

type CNSOutboundOptions struct {
	DialerOptions
	ServerOptions
	Key      string               `json:"key,omitempty"`
	Password string               `json:"password,omitempty"`
	Flag     string               `json:"flag,omitempty"`
	Network  NetworkList          `json:"network,omitempty"`
	Headers  badoption.HTTPHeader `json:"headers,omitempty"`
}
