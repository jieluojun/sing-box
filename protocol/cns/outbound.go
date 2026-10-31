package cns

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/dialer"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	TC "github.com/sagernet/sing-box/transport/cns"
	"github.com/sagernet/sing/common/buf"
	SBufio "github.com/sagernet/sing/common/bufio"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

func RegisterOutbound(registry *outbound.Registry) {
	outbound.Register[option.CNSOutboundOptions](registry, C.TypeCNS, NewOutbound)
}

type Outbound struct {
	outbound.Adapter
	dialer              N.Dialer
	dns                 adapter.DNSRouter
	logger              logger.ContextLogger
	server              M.Socksaddr
	key, password, flag string
	headers             http.Header
}

func NewOutbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.CNSOutboundOptions) (adapter.Outbound, error) {
	key, flag, err := defaults(options.Key, options.Flag)
	if err != nil {
		return nil, err
	}
	if !options.ServerOptions.Build().IsValid() || options.ServerPort == 0 {
		return nil, errors.New("invalid CNS server")
	}
	outboundDialer, err := dialer.New(ctx, options.DialerOptions, options.ServerIsDomain())
	if err != nil {
		return nil, err
	}
	headers := options.Headers.Build()
	if _, err = requestHeader(options.Server, "example.com:443", key, options.Password, flag, headers, true); err != nil {
		return nil, err
	}
	return &Outbound{
		Adapter: outbound.NewAdapterWithDialerOptions(C.TypeCNS, tag, options.Network.Build(), options.DialerOptions),
		dialer:  outboundDialer, dns: service.FromContext[adapter.DNSRouter](ctx), logger: logger,
		server: options.ServerOptions.Build(), key: key, password: options.Password, flag: flag, headers: headers,
	}, nil
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

func (o *Outbound) handshake(ctx context.Context, destination M.Socksaddr, udp bool) (net.Conn, error) {
	if !destination.IsValid() || destination.Port == 0 {
		return nil, errors.New("invalid CNS destination")
	}
	request, err := requestHeader(o.server.AddrString(), destination.String(), o.key, o.password, o.flag, o.headers, udp)
	if err != nil {
		return nil, err
	}
	conn, err := o.dialer.DialContext(ctx, N.NetworkTCP, o.server)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if err = writeFull(conn, []byte(request)); err != nil {
		conn.Close()
		return nil, E.Cause(err, "write CNS handshake")
	}
	reader := bufio.NewReaderSize(conn, maxHeaders)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil {
		conn.Close()
		return nil, E.Cause(err, "read CNS handshake")
	}
	if response.StatusCode != http.StatusOK {
		conn.Close()
		return nil, E.New("CNS handshake rejected: ", response.Status)
	}
	if !stop() {
		conn.Close()
		return nil, ctx.Err()
	}
	return &bufferedConn{conn, reader}, nil
}

func (o *Outbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	ctx, metadata := adapter.ExtendContext(ctx)
	metadata.Outbound = o.Tag()
	metadata.Destination = destination
	switch N.NetworkName(network) {
	case N.NetworkTCP:
		o.logger.InfoContext(ctx, "outbound connection to ", destination)
		conn, err := o.handshake(ctx, destination, false)
		if err != nil {
			return nil, err
		}
		return TC.NewCnsConn(conn, o.password), nil
	case N.NetworkUDP:
		// CNS frames carry IP addresses only. Resolve with sing-box DNS before binding.
		resolved, err := o.resolve(ctx, destination)
		if err != nil {
			return nil, err
		}
		pc, err := o.ListenPacket(ctx, resolved)
		if err != nil {
			return nil, err
		}
		return SBufio.NewBindPacketConn(pc, resolved.UDPAddr()), nil
	default:
		return nil, E.Extend(N.ErrUnknownNetwork, network)
	}
}

func (o *Outbound) resolve(ctx context.Context, destination M.Socksaddr) (M.Socksaddr, error) {
	if !destination.IsDomain() {
		return destination, nil
	}
	if o.dns == nil {
		return M.Socksaddr{}, errors.New("CNS DNS router unavailable")
	}
	addresses, err := o.dns.Lookup(ctx, destination.Fqdn, adapter.DNSQueryOptions{})
	if err != nil {
		return M.Socksaddr{}, err
	}
	if len(addresses) == 0 {
		return M.Socksaddr{}, errors.New("CNS DNS returned no addresses")
	}
	return M.SocksaddrFrom(addresses[0], destination.Port), nil
}

func (o *Outbound) ListenPacketWithDestination(ctx context.Context, destination M.Socksaddr) (net.PacketConn, netip.Addr, error) {
	ctx, metadata := adapter.ExtendContext(ctx)
	metadata.Outbound = o.Tag()
	metadata.Destination = destination
	o.logger.InfoContext(ctx, "outbound packet connection to ", destination)
	resolved, err := o.resolve(ctx, destination)
	if err != nil {
		return nil, netip.Addr{}, err
	}
	conn, err := o.handshake(ctx, resolved, true)
	if err != nil {
		return nil, netip.Addr{}, err
	}
	return &resolvingPacketConn{PacketConn: TC.NewUDPPacketConn(conn, o.password), outbound: o, ctx: ctx}, resolved.Addr, nil
}

func (o *Outbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	pc, address, err := o.ListenPacketWithDestination(ctx, destination)
	if err != nil {
		return nil, err
	}
	resolved := M.SocksaddrFrom(address, destination.Port)
	if address.IsValid() && resolved != destination {
		return SBufio.NewNATPacketConn(SBufio.NewPacketConn(pc), resolved, destination), nil
	}
	return pc, nil
}

type resolvingPacketConn struct {
	net.PacketConn
	outbound *Outbound
	ctx      context.Context
}

func (c *resolvingPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	if addr == nil {
		return 0, errors.New("nil CNS destination")
	}
	destination := M.SocksaddrFromNet(addr)
	if destination.IsDomain() {
		resolved, err := c.outbound.resolve(c.ctx, destination)
		if err != nil {
			return 0, err
		}
		addr = resolved.UDPAddr()
	}
	return c.PacketConn.WriteTo(p, addr)
}

func (c *resolvingPacketConn) ReadPacket(buffer *buf.Buffer) (M.Socksaddr, error) {
	n, addr, err := c.PacketConn.ReadFrom(buffer.FreeBytes())
	if err != nil {
		return M.Socksaddr{}, err
	}
	buffer.Truncate(n)
	return M.SocksaddrFromNet(addr).Unwrap(), nil
}

func (c *resolvingPacketConn) WritePacket(buffer *buf.Buffer, destination M.Socksaddr) error {
	defer buffer.Release()
	resolved, err := c.outbound.resolve(c.ctx, destination)
	if err != nil {
		return err
	}
	_, err = c.PacketConn.WriteTo(buffer.Bytes(), resolved.UDPAddr())
	return err
}
