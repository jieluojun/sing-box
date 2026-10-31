package cns

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	TC "github.com/sagernet/sing-box/transport/cns"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/json"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

func TestRequestHeaders(t *testing.T) {
	key, flag, err := defaults("", "")
	if err != nil {
		t.Fatal(err)
	}
	h, err := requestHeader("server.example", "target.example:443", key, "secret", flag, http.Header{"Host": {"cover.example"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "CONNECT cover.example:443 HTTP/1.1\r\n") {
		t.Fatal(h)
	}
	lines, err := readHeaders(bufio.NewReader(strings.NewReader(h)))
	if err != nil {
		t.Fatal(err)
	}
	target, udp, err := parseHeaders(lines, key, flag, "secret")
	if err != nil || target != "" || !udp {
		t.Fatalf("%q %v %v", target, udp, err)
	}
	tcpHeader, err := requestHeader("server", "target.example:443", key, "secret", flag, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	tcpLines, err := readHeaders(bufio.NewReader(strings.NewReader(tcpHeader)))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = parseHeaders(tcpLines, key, flag, "wrong"); err == nil {
		t.Fatal("accepted wrong password")
	}
	if _, err = requestHeader("server", "target:443", key, "", flag, http.Header{"X-Test": {"bad\r\nInjected: yes"}}, false); err == nil {
		t.Fatal("accepted header injection")
	}
}

type echoRouter struct {
	adapter.ConnectionRouterEx
	targets chan M.Socksaddr
}

func (r *echoRouter) RouteConnectionEx(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	r.targets <- metadata.Destination
	go func() {
		defer conn.Close()
		io.Copy(conn, conn)
		if onClose != nil {
			onClose(nil)
		}
	}()
}
func (r *echoRouter) RoutePacketConnectionEx(ctx context.Context, conn N.PacketConn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	r.targets <- metadata.Destination
	go func() {
		defer conn.Close()
		defer onClose(nil)
		for {
			buffer := buf.NewPacket()
			dest, err := conn.ReadPacket(buffer)
			if err != nil {
				buffer.Release()
				return
			}
			if err := conn.WritePacket(buffer, dest); err != nil {
				return
			}
		}
	}()
}

func TestClientServerTCPAndUDP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	router := &echoRouter{targets: make(chan M.Socksaddr, 5)}
	server := &Inbound{Adapter: inbound.NewAdapter("cns", "in"), router: router, logger: log.NewNOPFactory().Logger(), key: defaultKey, flag: defaultFlag, password: "secret", udpTimeout: time.Minute}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				if err := server.handle(context.Background(), conn, adapter.InboundContext{}, nil); err != nil {
					conn.Close()
				}
			}()
		}
	}()
	client := &Outbound{dialer: N.SystemDialer, server: M.SocksaddrFromNet(ln.Addr()), key: defaultKey, flag: defaultFlag, password: "secret"}
	dest := M.ParseSocksaddr("example.com:443")
	conn, err := client.handshake(context.Background(), dest, false)
	if err != nil {
		t.Fatal(err)
	}
	conn = TC.NewCnsConn(conn, "secret")
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 5)
	if _, err := io.ReadFull(conn, b); err != nil || string(b) != "hello" {
		t.Fatalf("TCP echo %q: %v", b, err)
	}
	if got := <-router.targets; got != dest {
		t.Fatalf("TCP destination: %v", got)
	}

	raw, err := client.handshake(context.Background(), dest, true)
	if err != nil {
		t.Fatal(err)
	}
	pc := TC.NewUDPPacketConn(raw, "secret")
	defer pc.Close()
	pc.SetDeadline(time.Now().Add(5 * time.Second))
	for _, addr := range []*net.UDPAddr{{IP: net.IPv4(1, 1, 1, 1), Port: 53}, {IP: net.ParseIP("2001:db8::1"), Port: 1234}} {
		if _, err := pc.WriteTo([]byte("udp"), addr); err != nil {
			t.Fatal(err)
		}
		p := make([]byte, 20)
		n, gotAddr, err := pc.ReadFrom(p)
		if err != nil || string(p[:n]) != "udp" || gotAddr.String() != addr.String() {
			t.Fatalf("UDP echo %q %v: %v", p[:n], gotAddr, err)
		}
		if got := <-router.targets; got != M.SocksaddrFromNet(addr).Unwrap() {
			t.Fatalf("UDP destination: %v", got)
		}
	}
}

func TestUDPHandshakeWithoutDestinationHeader(t *testing.T) {
	lines, err := readHeaders(bufio.NewReader(strings.NewReader("CONNECT / HTTP/1.1\r\nhttpUDP\r\nHost: server\r\n\r\n")))
	if err != nil {
		t.Fatal(err)
	}
	_, udp, err := parseHeaders(lines, "Meng", "httpUDP", "secret")
	if err != nil || !udp {
		t.Fatalf("legacy UDP handshake: udp=%v err=%v", udp, err)
	}
}

func TestOversizedHandshake(t *testing.T) {
	reader := bufio.NewReaderSize(strings.NewReader("CONNECT / HTTP/1.1\r\nX: "+strings.Repeat("a", maxHeaders)+"\r\n\r\n"), maxHeaders+1)
	if _, err := readHeaders(reader); err == nil {
		t.Fatal("accepted oversized handshake")
	}
}

type testDNS struct{ adapter.DNSRouter }

func (d testDNS) Lookup(ctx context.Context, domain string, options adapter.DNSQueryOptions) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("192.0.2.10")}, nil
}

func TestRegistriesAndOptions(t *testing.T) {
	in := inbound.NewRegistry()
	RegisterInbound(in)
	out := outbound.NewRegistry()
	RegisterOutbound(out)
	inOpts, ok := in.CreateOptions("cns")
	if !ok {
		t.Fatal("missing inbound registration")
	}
	if err := json.UnmarshalContext(context.Background(), []byte(`{"listen_port":23333,"key":"Meng","password":"secret","flag":"httpUDP"}`), inOpts); err != nil {
		t.Fatal(err)
	}
	if inOpts.(*option.CNSInboundOptions).ListenPort != 23333 {
		t.Fatal("invalid inbound options")
	}
	outOpts, ok := out.CreateOptions("cns")
	if !ok {
		t.Fatal("missing outbound registration")
	}
	if err := json.UnmarshalContext(context.Background(), []byte(`{"server":"example.com","server_port":23333,"network":["tcp","udp"],"headers":{"Host":"cover.example"}}`), outOpts); err != nil {
		t.Fatal(err)
	}
	if outOpts.(*option.CNSOutboundOptions).Server != "example.com" {
		t.Fatal("invalid outbound options")
	}
}

func TestUDPDomainResolution(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	c := &resolvingPacketConn{PacketConn: TC.NewUDPPacketConn(client, "secret"), outbound: &Outbound{dns: testDNS{}}, ctx: context.Background()}
	done := make(chan error, 1)
	go func() { done <- c.WritePacket(buf.As([]byte("payload")), M.ParseSocksaddr("example.com:53")) }()
	receiver := TC.NewUDPPacketConn(server, "secret")
	data := make([]byte, 32)
	n, addr, err := receiver.ReadFrom(data)
	if err != nil || string(data[:n]) != "payload" || addr.String() != "192.0.2.10:53" {
		t.Fatalf("packet %q %v %v", data[:n], addr, err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}
