package parser

import (
	"context"
	"reflect"
	"strings"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/json/badoption"
)

var subscriptionParsers = []func(ctx context.Context, content string) ([]option.Outbound, []option.Endpoint, error){
	ParseBoxSubscription,
	ParseClashSubscription,
	ParseSIP008Subscription,
	ParseRawSubscription,
}

func ParseSubscription(ctx context.Context, content string, overrideDialerOptions *option.OverrideDialerOptions, overrideTLSOptions *option.OverrideTLSOptions, overrideAnyTLSOptions *option.OverrideAnyTLSOptions, overrideTransportOptions *option.OverrideTransportOptions, providerTag string) ([]option.Outbound, []option.Endpoint, error) {
	var pErr error
	for _, parser := range subscriptionParsers {
		outbounds, endpoints, err := parser(ctx, content)
		if len(outbounds) > 0 || len(endpoints) > 0 {
			tags := providerTags(outbounds, endpoints)
			return overrideOutbounds(outbounds, overrideDialerOptions, overrideTLSOptions, overrideAnyTLSOptions, overrideTransportOptions, tags, providerTag),
				overrideEndpoints(endpoints, overrideDialerOptions, tags, providerTag),
				nil
		}
		pErr = E.Errors(pErr, err)
	}
	return nil, nil, E.Cause(pErr, "no servers found")
}

func providerTags(outbounds []option.Outbound, endpoints []option.Endpoint) []string {
	tags := make([]string, 0, len(outbounds)+len(endpoints))
	for _, outbound := range outbounds {
		tags = append(tags, outbound.Tag)
	}
	for _, endpoint := range endpoints {
		tags = append(tags, endpoint.Tag)
	}
	return tags
}

func overrideOutbounds(outbounds []option.Outbound, overrideDialerOptions *option.OverrideDialerOptions, overrideTLSOptions *option.OverrideTLSOptions, overrideAnyTLSOptions *option.OverrideAnyTLSOptions, overrideTransportOptions *option.OverrideTransportOptions, tags []string, providerTag string) []option.Outbound {
	var parsedOutbounds []option.Outbound
	for _, outbound := range outbounds {
		switch outbound.Type {
		case C.TypeHTTP:
			options := outbound.Options.(*option.HTTPOutboundOptions)
			options.DialerOptions = overrideDialerOption(options.DialerOptions, overrideDialerOptions, tags, providerTag)
			options.OutboundTLSOptionsContainer.TLS = overrideTLSOption(options.OutboundTLSOptionsContainer.TLS, overrideTLSOptions)
			if overrideTransportOptions != nil {
				if overrideTransportOptions.Host != nil {
					options.Headers = overrideHostHeader(options.Headers, *overrideTransportOptions.Host)
				}
				if overrideTransportOptions.Path != nil {
					options.Path = *overrideTransportOptions.Path
				}
				withAt := overrideTransportOptions.WithAt
				if withAt == nil {
					withAt = overrideTransportOptions.WithAtHeader
				}
				if withAt != nil {
					options.Headers = overrideWithAtHeader(options.Headers, *withAt)
				}
			}
			outbound.Options = options
		case C.TypeCNS:
			options := outbound.Options.(*option.CNSOutboundOptions)
			options.DialerOptions = overrideDialerOption(options.DialerOptions, overrideDialerOptions, tags, providerTag)
			if overrideTransportOptions != nil && overrideTransportOptions.Host != nil {
				options.Headers = overrideHostHeader(options.Headers, *overrideTransportOptions.Host)
			}
			outbound.Options = options
		case C.TypeSOCKS:
			options := outbound.Options.(*option.SOCKSOutboundOptions)
			options.DialerOptions = overrideDialerOption(options.DialerOptions, overrideDialerOptions, tags, providerTag)
			outbound.Options = options
		case C.TypeTUIC:
			options := outbound.Options.(*option.TUICOutboundOptions)
			options.DialerOptions = overrideDialerOption(options.DialerOptions, overrideDialerOptions, tags, providerTag)
			options.OutboundTLSOptionsContainer.TLS = overrideTLSOption(options.OutboundTLSOptionsContainer.TLS, overrideTLSOptions)
			outbound.Options = options
		case C.TypeVMess:
			options := outbound.Options.(*option.VMessOutboundOptions)
			options.DialerOptions = overrideDialerOption(options.DialerOptions, overrideDialerOptions, tags, providerTag)
			options.OutboundTLSOptionsContainer.TLS = overrideTLSOption(options.OutboundTLSOptionsContainer.TLS, overrideTLSOptions)
			options.Transport = overrideTransportOption(options.Transport, overrideTLSOptions, overrideTransportOptions)
			outbound.Options = options
		case C.TypeVLESS:
			options := outbound.Options.(*option.VLESSOutboundOptions)
			options.DialerOptions = overrideDialerOption(options.DialerOptions, overrideDialerOptions, tags, providerTag)
			options.OutboundTLSOptionsContainer.TLS = overrideTLSOption(options.OutboundTLSOptionsContainer.TLS, overrideTLSOptions)
			options.Transport = overrideTransportOption(options.Transport, overrideTLSOptions, overrideTransportOptions)
			outbound.Options = options
		case C.TypeTrojan:
			options := outbound.Options.(*option.TrojanOutboundOptions)
			options.DialerOptions = overrideDialerOption(options.DialerOptions, overrideDialerOptions, tags, providerTag)
			options.OutboundTLSOptionsContainer.TLS = overrideTLSOption(options.OutboundTLSOptionsContainer.TLS, overrideTLSOptions)
			options.Transport = overrideTransportOption(options.Transport, overrideTLSOptions, overrideTransportOptions)
			outbound.Options = options
		case C.TypeHysteria:
			options := outbound.Options.(*option.HysteriaOutboundOptions)
			options.DialerOptions = overrideDialerOption(options.DialerOptions, overrideDialerOptions, tags, providerTag)
			options.OutboundTLSOptionsContainer.TLS = overrideTLSOption(options.OutboundTLSOptionsContainer.TLS, overrideTLSOptions)
			outbound.Options = options
		case C.TypeShadowTLS:
			options := outbound.Options.(*option.ShadowTLSOutboundOptions)
			options.DialerOptions = overrideDialerOption(options.DialerOptions, overrideDialerOptions, tags, providerTag)
			options.OutboundTLSOptionsContainer.TLS = overrideTLSOption(options.OutboundTLSOptionsContainer.TLS, overrideTLSOptions)
			outbound.Options = options
		case C.TypeHysteria2:
			options := outbound.Options.(*option.Hysteria2OutboundOptions)
			options.DialerOptions = overrideDialerOption(options.DialerOptions, overrideDialerOptions, tags, providerTag)
			options.OutboundTLSOptionsContainer.TLS = overrideTLSOption(options.OutboundTLSOptionsContainer.TLS, overrideTLSOptions)
			outbound.Options = options
		case C.TypeQUICX:
			options := outbound.Options.(*option.QUICXOutboundOptions)
			options.DialerOptions = overrideDialerOption(options.DialerOptions, overrideDialerOptions, tags, providerTag)
			options.OutboundTLSOptionsContainer.TLS = overrideTLSOption(options.OutboundTLSOptionsContainer.TLS, overrideTLSOptions)
			outbound.Options = options
		case C.TypeNowhere:
			options := outbound.Options.(*option.NowhereOutboundOptions)
			options.DialerOptions = overrideDialerOption(options.DialerOptions, overrideDialerOptions, tags, providerTag)
			options.OutboundTLSOptionsContainer.TLS = overrideTLSOption(options.OutboundTLSOptionsContainer.TLS, overrideTLSOptions)
			outbound.Options = options
		case C.TypeAnyTLS:
			anytlsOptions := outbound.Options.(*option.AnyTLSOutboundOptions)
			dialerOptions := overrideDialerOption(anytlsOptions.DialerOptions, overrideDialerOptions, tags, providerTag)
			tlsOptions := overrideTLSOption(anytlsOptions.OutboundTLSOptionsContainer.TLS, overrideTLSOptions)
			if overrideAnyTLSOptions != nil && overrideAnyTLSOptions.UseAnyX != nil && *overrideAnyTLSOptions.UseAnyX {
				anyxOptions := &option.AnyXOutboundOptions{
					DialerOptions:               dialerOptions,
					ServerOptions:               anytlsOptions.ServerOptions,
					OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{TLS: tlsOptions},
					Password:                    anytlsOptions.Password,
					IdleSessionCheckInterval:    anytlsOptions.IdleSessionCheckInterval,
					IdleSessionTimeout:          anytlsOptions.IdleSessionTimeout,
					MinIdleSession:              anytlsOptions.MinIdleSession,
					ClientMetadata:              anytlsOptions.ClientMetadata,
					DisableReuse:                anytlsOptions.DisableReuse,
					Network:                     anytlsOptions.Network,
				}
				overrideAnyXOptions(anyxOptions, overrideAnyTLSOptions)
				outbound.Type = C.TypeAnyX
				outbound.Options = anyxOptions
			} else {
				anytlsOptions.DialerOptions = dialerOptions
				anytlsOptions.OutboundTLSOptionsContainer.TLS = tlsOptions
				if overrideAnyTLSOptions != nil {
					if overrideAnyTLSOptions.ClientMetadata != nil {
						anytlsOptions.ClientMetadata = overrideAnyTLSOptions.ClientMetadata
					}
					if overrideAnyTLSOptions.DisableReuse != nil {
						anytlsOptions.DisableReuse = *overrideAnyTLSOptions.DisableReuse
					}
					if overrideAnyTLSOptions.MinIdleSession != nil {
						anytlsOptions.MinIdleSession = *overrideAnyTLSOptions.MinIdleSession
					}
					if overrideAnyTLSOptions.IdleSessionTimeout != nil {
						anytlsOptions.IdleSessionTimeout = *overrideAnyTLSOptions.IdleSessionTimeout
					}
				}
				outbound.Options = anytlsOptions
			}
		case C.TypeAnyX:
			options := outbound.Options.(*option.AnyXOutboundOptions)
			options.DialerOptions = overrideDialerOption(options.DialerOptions, overrideDialerOptions, tags, providerTag)
			options.OutboundTLSOptionsContainer.TLS = overrideTLSOption(options.OutboundTLSOptionsContainer.TLS, overrideTLSOptions)
			if overrideAnyTLSOptions != nil {
				overrideAnyXOptions(options, overrideAnyTLSOptions)
			}
			outbound.Options = options
		case C.TypeShadowsocks:
			options := outbound.Options.(*option.ShadowsocksOutboundOptions)
			options.DialerOptions = overrideDialerOption(options.DialerOptions, overrideDialerOptions, tags, providerTag)
			outbound.Options = options
		case C.TypeSnell:
			options := outbound.Options.(*option.SnellOutboundOptions)
			options.DialerOptions = overrideDialerOption(options.DialerOptions, overrideDialerOptions, tags, providerTag)
			outbound.Options = options
		}
		parsedOutbounds = append(parsedOutbounds, outbound)
	}
	return parsedOutbounds
}

func overrideEndpoints(endpoints []option.Endpoint, overrideDialerOptions *option.OverrideDialerOptions, tags []string, providerTag string) []option.Endpoint {
	if len(endpoints) == 0 {
		return nil
	}
	var parsedEndpoints []option.Endpoint
	for _, ep := range endpoints {
		switch ep.Type {
		case C.TypeWireGuard:
			options := ep.Options.(*option.WireGuardEndpointOptions)
			options.DialerOptions = overrideDialerOption(options.DialerOptions, overrideDialerOptions, tags, providerTag)
			ep.Options = options
		case C.TypeTailscale:
			options := ep.Options.(*option.TailscaleEndpointOptions)
			options.DialerOptions = overrideDialerOption(options.DialerOptions, overrideDialerOptions, tags, providerTag)
			ep.Options = options
		}
		parsedEndpoints = append(parsedEndpoints, ep)
	}
	return parsedEndpoints
}

func overrideDialerOption(options option.DialerOptions, overrideDialerOptions *option.OverrideDialerOptions, tags []string, providerTag string) option.DialerOptions {
	if options.Detour != "" {
		if common.Any(tags, func(tag string) bool {
			return options.Detour == tag
		}) {
			if providerTag != "" {
				options.Detour = providerTag + "/" + options.Detour
			}
		} else {
			options.Detour = ""
		}
	}
	var defaultOptions option.OverrideDialerOptions
	if overrideDialerOptions == nil || reflect.DeepEqual(*overrideDialerOptions, defaultOptions) {
		return options
	}
	if overrideDialerOptions.Detour != nil && options.Detour == "" {
		options.Detour = *overrideDialerOptions.Detour
	}
	if overrideDialerOptions.BindInterface != nil {
		options.BindInterface = *overrideDialerOptions.BindInterface
	}
	if overrideDialerOptions.Inet4BindAddress != nil {
		options.Inet4BindAddress = overrideDialerOptions.Inet4BindAddress
	}
	if overrideDialerOptions.Inet6BindAddress != nil {
		options.Inet6BindAddress = overrideDialerOptions.Inet6BindAddress
	}
	if overrideDialerOptions.ProtectPath != nil {
		options.ProtectPath = *overrideDialerOptions.ProtectPath
	}
	if overrideDialerOptions.RoutingMark != nil {
		options.RoutingMark = *overrideDialerOptions.RoutingMark
	}
	if overrideDialerOptions.ReuseAddr != nil {
		options.ReuseAddr = *overrideDialerOptions.ReuseAddr
	}
	if overrideDialerOptions.ConnectTimeout != nil {
		options.ConnectTimeout = *overrideDialerOptions.ConnectTimeout
	}
	if overrideDialerOptions.TCPFastOpen != nil {
		options.TCPFastOpen = *overrideDialerOptions.TCPFastOpen
	}
	if overrideDialerOptions.TCPMultiPath != nil {
		options.TCPMultiPath = *overrideDialerOptions.TCPMultiPath
	}
	if overrideDialerOptions.TCPKeepAlive != nil {
		options.TCPKeepAlive = *overrideDialerOptions.TCPKeepAlive
	}
	if overrideDialerOptions.TCPKeepAliveInterval != nil {
		options.TCPKeepAliveInterval = *overrideDialerOptions.TCPKeepAliveInterval
	}
	if overrideDialerOptions.UDPGSO != nil {
		options.UDPGSO = overrideDialerOptions.UDPGSO
	}
	if overrideDialerOptions.UDPFragment != nil {
		options.UDPFragment = overrideDialerOptions.UDPFragment
	}
	if overrideDialerOptions.DomainResolver != nil {
		options.DomainResolver = overrideDialerOptions.DomainResolver
	}
	if overrideDialerOptions.NetworkStrategy != nil {
		options.NetworkStrategy = overrideDialerOptions.NetworkStrategy
	}
	if overrideDialerOptions.NetworkType != nil {
		options.NetworkType = *overrideDialerOptions.NetworkType
	}
	if overrideDialerOptions.FallbackNetworkType != nil {
		options.FallbackNetworkType = *overrideDialerOptions.FallbackNetworkType
	}
	if overrideDialerOptions.FallbackDelay != nil {
		options.FallbackDelay = *overrideDialerOptions.FallbackDelay
	}
	if overrideDialerOptions.TCPKeepAliveCount != nil {
		options.TCPKeepAliveCount = *overrideDialerOptions.TCPKeepAliveCount
	}
	if overrideDialerOptions.DisableTCPKeepAlive != nil {
		options.DisableTCPKeepAlive = *overrideDialerOptions.DisableTCPKeepAlive
	}

	//nolint:staticcheck
	if overrideDialerOptions.DomainStrategy != nil {
		options.DomainStrategy = *overrideDialerOptions.DomainStrategy
	}
	return options
}

func overrideRealityOption(reality *option.OutboundRealityOptions, overrideReality *option.OverrideRealityOptions) {
	if reality == nil || overrideReality == nil {
		return
	}
	if overrideReality.KeyShare != nil {
		reality.KeyShare = *overrideReality.KeyShare
	}
	if overrideReality.Version != nil {
		reality.Version = *overrideReality.Version
	}
}

func overrideTLSOption(options *option.OutboundTLSOptions, overrideTLSOptions *option.OverrideTLSOptions) *option.OutboundTLSOptions {
	if options == nil {
		return options
	}
	var defaultOptions option.OutboundTLSOptions
	if overrideTLSOptions == nil || reflect.DeepEqual(*overrideTLSOptions, defaultOptions) {
		return options
	}
	if overrideTLSOptions.Enabled != nil && !*overrideTLSOptions.Enabled {
		return &defaultOptions
	}
	// if override.OverrideTLSOptions.Enabled != nil {
	// options.Enabled = *override.OverrideTLSOptions.Enabled
	// }
	if overrideTLSOptions.DisableSNI != nil {
		options.DisableSNI = *overrideTLSOptions.DisableSNI
	}
	if overrideTLSOptions.ServerName != nil {
		options.ServerName = *overrideTLSOptions.ServerName
	}
	if overrideTLSOptions.CertificateServerName != nil {
		options.CertificateServerName = *overrideTLSOptions.CertificateServerName
	}
	if overrideTLSOptions.Insecure != nil {
		options.Insecure = *overrideTLSOptions.Insecure
	}
	if overrideTLSOptions.KernelTx != nil {
		options.KernelTx = *overrideTLSOptions.KernelTx
	}
	if overrideTLSOptions.KernelRx != nil {
		options.KernelRx = *overrideTLSOptions.KernelRx
	}
	if overrideTLSOptions.ECH != nil {
		options.ECH = overrideECHOption(options.ECH, overrideTLSOptions.ECH)
	}
	if overrideTLSOptions.Reality != nil && options.Reality != nil {
		overrideRealityOption(options.Reality, overrideTLSOptions.Reality)
	}
	return options
}

// Remove all case variants so a stale header cannot override the chosen host.
func removeHostHeader(headers badoption.HTTPHeader) badoption.HTTPHeader {
	for key := range headers {
		if strings.EqualFold(key, "Host") {
			delete(headers, key)
		}
	}
	return headers
}

func overrideHostHeader(headers badoption.HTTPHeader, host string) badoption.HTTPHeader {
	if headers == nil {
		headers = make(badoption.HTTPHeader)
	}
	removeHostHeader(headers)
	headers["Host"] = badoption.Listable[string]{host}
	return headers
}

// With-At is a CONNECT target option, not a header sent to the server.
func overrideWithAtHeader(headers badoption.HTTPHeader, value string) badoption.HTTPHeader {
	for key := range headers {
		if strings.EqualFold(key, "With-At") {
			delete(headers, key)
		}
	}
	if value != "" {
		if headers == nil {
			headers = make(badoption.HTTPHeader)
		}
		headers["With-At"] = badoption.Listable[string]{value}
	}
	return headers
}

func overrideTransportOption(options *option.V2RayTransportOptions, overrideTLSOptions *option.OverrideTLSOptions, overrideTransportOptions *option.OverrideTransportOptions) *option.V2RayTransportOptions {
	if options == nil {
		return nil
	}
	if overrideTransportOptions != nil && overrideTransportOptions.Host != nil {
		host := *overrideTransportOptions.Host
		switch options.Type {
		case C.V2RayTransportTypeWebsocket:
			options.WebsocketOptions.Headers = overrideHostHeader(options.WebsocketOptions.Headers, host)
		case C.V2RayTransportTypeHTTP:
			options.HTTPOptions.Host = badoption.Listable[string]{host}
			options.HTTPOptions.Headers = removeHostHeader(options.HTTPOptions.Headers)
		case C.V2RayTransportTypeHTTPUpgrade:
			options.HTTPUpgradeOptions.Host = host
			options.HTTPUpgradeOptions.Headers = removeHostHeader(options.HTTPUpgradeOptions.Headers)
		case C.V2RayTransportTypeXHTTP:
			if xhttpOptions, ok := options.Extra.(*option.V2RayXHTTPOptions); ok && xhttpOptions != nil {
				xhttpOptions.Host = badoption.Listable[string]{host}
				xhttpOptions.Headers = removeHostHeader(xhttpOptions.Headers)
			}
		}
	}
	if options.Type != C.V2RayTransportTypeXHTTP {
		return options
	}
	xhttpOptions, ok := options.Extra.(*option.V2RayXHTTPOptions)
	if !ok || xhttpOptions == nil {
		return options
	}
	if overrideTLSOptions != nil && overrideTLSOptions.Reality != nil && xhttpOptions.DownloadSettings != nil && xhttpOptions.DownloadSettings.TLS != nil && xhttpOptions.DownloadSettings.TLS.Reality != nil {
		overrideRealityOption(xhttpOptions.DownloadSettings.TLS.Reality, overrideTLSOptions.Reality)
	}
	if overrideTransportOptions == nil {
		return options
	}
	if overrideTransportOptions.XHTTPDownloadEnabled != nil {
		if xhttpOptions.DownloadSettings != nil {
			xhttpOptions.DownloadSettings.Enabled = overrideTransportOptions.XHTTPDownloadEnabled
		}
	}
	if overrideTransportOptions.XHTTPDownloadDetour != nil {
		if xhttpOptions.DownloadSettings != nil {
			xhttpOptions.DownloadSettings.Detour = *overrideTransportOptions.XHTTPDownloadDetour
		}
	}
	return options
}

func overrideECHOption(options *option.OutboundECHOptions, overrideECHOptions *option.OverrideECHOptions) *option.OutboundECHOptions {
	var newOptions option.OutboundECHOptions
	if options != nil {
		newOptions = *options
	}
	if overrideECHOptions.Enabled != nil {
		newOptions.Enabled = *overrideECHOptions.Enabled
	}
	if overrideECHOptions.Config != nil {
		newOptions.Config = *overrideECHOptions.Config
	}
	if overrideECHOptions.ConfigPath != nil {
		newOptions.ConfigPath = *overrideECHOptions.ConfigPath
		// inline config takes precedence over config_path, drop the node's own one
		if overrideECHOptions.Config == nil {
			newOptions.Config = nil
		}
	}
	if overrideECHOptions.QueryServerName != nil {
		newOptions.QueryServerName = *overrideECHOptions.QueryServerName
	}
	return &newOptions
}

type warnHandlerKey struct{}

// ContextWithWarnHandler lets parsers report subscription nodes they have to
// skip, such as a Clash proxy whose protocol this build does not implement.
func ContextWithWarnHandler(ctx context.Context, handler func(args ...any)) context.Context {
	return context.WithValue(ctx, warnHandlerKey{}, handler)
}

func warn(ctx context.Context, args ...any) {
	if handler, loaded := ctx.Value(warnHandlerKey{}).(func(args ...any)); loaded {
		handler(args...)
	}
}

func overrideAnyXOptions(options *option.AnyXOutboundOptions, overrideOptions *option.OverrideAnyTLSOptions) {
	if overrideOptions.ClientMetadata != nil {
		options.ClientMetadata = overrideOptions.ClientMetadata
	}
	if overrideOptions.DisableReuse != nil {
		options.DisableReuse = *overrideOptions.DisableReuse
	}
	if overrideOptions.PaddingScheme != nil {
		options.PaddingScheme = *overrideOptions.PaddingScheme
	}
	if overrideOptions.PaddingSchemeInterval != nil {
		options.PaddingSchemeInterval = *overrideOptions.PaddingSchemeInterval
	}
	if overrideOptions.MinIdleSession != nil {
		options.MinIdleSession = *overrideOptions.MinIdleSession
	}
	if overrideOptions.IdleSessionTimeout != nil {
		options.IdleSessionTimeout = *overrideOptions.IdleSessionTimeout
	}
	if overrideOptions.IdleSessionMinTimeout != nil {
		options.IdleSessionMinTimeout = *overrideOptions.IdleSessionMinTimeout
	}
}
