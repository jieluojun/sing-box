package cns

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/common/listener"
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
	"github.com/sagernet/sing/common/udpnat"
)

func RegisterInbound(registry *inbound.Registry) {
	inbound.Register[option.CNSInboundOptions](registry, C.TypeCNS, NewInbound)
}

type Inbound struct {
	inbound.Adapter
	router              adapter.ConnectionRouterEx
	logger              logger.ContextLogger
	listener            *listener.Listener
	key, password, flag string
	udpTimeout          time.Duration
}

func NewInbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.CNSInboundOptions) (adapter.Inbound, error) {
	key, flag, err := defaults(options.Key, options.Flag)
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(options.UDPTimeout)
	if timeout <= 0 {
		timeout = C.UDPTimeout
	}
	i := &Inbound{Adapter: inbound.NewAdapter(C.TypeCNS, tag), router: router, logger: logger, key: key, password: options.Password, flag: flag, udpTimeout: timeout}
	i.listener = listener.New(listener.Options{Context: ctx, Logger: logger, Network: []string{N.NetworkTCP}, Listen: options.ListenOptions, ConnectionHandler: i})
	return i, nil
}

func (i *Inbound) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	if stage != adapter.StartStateStart {
		return nil
	}
	if err := i.listener.Start(); err != nil {
		return err
	}
	scope.Add(i.listener.Close)
	return nil
}

func (i *Inbound) NewConnection(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	if err := i.handle(ctx, conn, metadata, onClose); err != nil {
		N.CloseOnHandshakeFailure(conn, onClose, err)
		if !E.IsClosedOrCanceled(err) {
			i.logger.ErrorContext(ctx, E.Cause(err, "CNS connection from ", metadata.Source))
		}
	}
}

func (i *Inbound) handle(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) error {
	if err := conn.SetDeadline(time.Now().Add(C.TCPTimeout)); err != nil {
		return err
	}
	reader := bufio.NewReaderSize(conn, maxHeaders+1)
	lines, err := readHeaders(reader)
	if err != nil {
		return err
	}
	host, udp, err := parseHeaders(lines, i.key, i.flag, i.password)
	if err != nil {
		return err
	}
	var destination M.Socksaddr
	if !udp {
		if _, _, err = net.SplitHostPort(host); err != nil {
			host = net.JoinHostPort(host, "80")
		}
		hostname, portText, err := net.SplitHostPort(host)
		if err != nil {
			return err
		}
		port, err := strconv.ParseUint(portText, 10, 16)
		if err != nil || port == 0 || hostname == "" || strings.IndexFunc(hostname, func(r rune) bool { return r <= 0x20 || r == 0x7f }) >= 0 {
			return errors.New("invalid CNS target")
		}
		destination = M.ParseSocksaddrHostPort(hostname, uint16(port)).Unwrap()
		if !destination.IsValid() {
			return errors.New("invalid CNS target")
		}
	}
	if err = writeFull(conn, established); err != nil {
		return err
	}
	if err = conn.SetDeadline(time.Time{}); err != nil {
		return err
	}
	wrapped := &bufferedConn{conn, reader}
	metadata.Inbound = i.Tag()
	metadata.InboundType = i.Type()
	metadata.Destination = destination
	if !udp {
		i.logger.InfoContext(ctx, "inbound connection to ", destination)
		i.router.RouteConnectionEx(ctx, TC.NewCnsConn(wrapped, i.password), metadata, onClose)
		return nil
	}
	// Each destination is routed independently, just as for ordinary UDP inbound traffic.
	i.handlePackets(ctx, TC.NewUDPPacketConn(wrapped, i.password), metadata, onClose)
	return nil
}

type packetHandler struct {
	inbound  *Inbound
	metadata adapter.InboundContext
}

func (h *packetHandler) NewPacketConnectionEx(ctx context.Context, conn N.PacketConn, source, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	metadata := h.metadata
	metadata.Source = source
	metadata.Destination = destination
	h.inbound.router.RoutePacketConnectionEx(ctx, conn, metadata, onClose)
}

type packetWriter struct{ N.PacketWriter }

func (i *Inbound) handlePackets(ctx context.Context, pc net.PacketConn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer pc.Close()
	service := udpnat.NewEx[M.Socksaddr](int64(i.udpTimeout.Seconds()), &packetHandler{i, metadata})
	writer := packetWriter{SBufio.NewPacketConn(pc)}
	for {
		buffer := buf.NewSize(65535)
		n, addr, err := pc.ReadFrom(buffer.FreeBytes())
		if err != nil {
			buffer.Release()
			if onClose != nil {
				onClose(err)
			}
			return
		}
		buffer.Truncate(n)
		destination := M.SocksaddrFromNet(addr).Unwrap()
		service.NewPacketEx(ctx, destination, buffer, metadata.Source, destination, func(N.PacketConn) N.PacketWriter { return writer })
	}
}
