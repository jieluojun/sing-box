package provider

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	providerregistry "github.com/sagernet/sing-box/adapter/provider"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	httpout "github.com/sagernet/sing-box/protocol/http"
	"github.com/sagernet/sing-box/protocol/vmess"
	"github.com/sagernet/sing/common/json"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/filemanager"
	"github.com/stretchr/testify/require"
)

type recordingOutboundManager struct {
	adapter.OutboundManager
	mu        sync.Mutex
	outbounds map[string]adapter.Outbound
	options   map[string]any
}

func (m *recordingOutboundManager) Outbound(tag string) (adapter.Outbound, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out, ok := m.outbounds[tag]
	return out, ok
}

func (m *recordingOutboundManager) Create(_ context.Context, _ adapter.Router, _ log.ContextLogger, tag, _ string, options any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.outbounds[tag] = &recordedOutbound{tag: tag}
	m.options[tag] = options
	return nil
}

func (m *recordingOutboundManager) Remove(tag string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.outbounds, tag)
	delete(m.options, tag)
	return nil
}

func (m *recordingOutboundManager) option(tag string) any {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.options[tag]
}

type recordedOutbound struct {
	adapter.Outbound
	tag string
}

func (o *recordedOutbound) Tag() string { return o.tag }
func (o *recordedOutbound) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	return nil, errors.New("health check disabled for test outbound")
}

func localOverrideProvider(t *testing.T, nodes, override string) (*ProviderLocal, context.Context, *recordingOutboundManager, string) {
	t.Helper()
	base := t.TempDir()
	path := filepath.Join(base, "proxies", "免流节点.txt")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(nodes), 0o600))
	parent, cancel := context.WithCancel(context.Background())
	ctx := filemanager.WithDefault(parent, base, "", os.Getuid(), os.Getgid())
	manager := &recordingOutboundManager{outbounds: make(map[string]adapter.Outbound), options: make(map[string]any)}
	ctx = service.ContextWith[adapter.OutboundManager](ctx, manager)
	ctx = service.ContextWith[adapter.EndpointManager](ctx, &localTestEndpointManager{})
	registry := outbound.NewRegistry()
	outbound.Register[option.VMessOutboundOptions](registry, C.TypeVMess, nil)
	outbound.Register[option.HTTPOutboundOptions](registry, C.TypeHTTP, nil)
	ctx = service.ContextWith[option.OutboundOptionsRegistry](ctx, registry)
	providerOptions := providerregistry.NewRegistry()
	RegisterProviderLocal(providerOptions)
	ctx = service.ContextWith[option.ProviderOptionsRegistry](ctx, providerOptions)
	config := fmt.Sprintf(`{"type":"local","tag":"免流节点","path":"./proxies/免流节点.txt","health_check":{"enabled":true,"interval":"0m","timeout":"5s"},"override_dialer":%s}`, override)
	var decoded option.Provider
	require.NoError(t, json.UnmarshalContextDisallowUnknownFields(ctx, []byte(config), &decoded))
	local := decoded.Options.(*option.ProviderLocalOptions)
	require.True(t, local.HealthCheck.Enabled)
	p, err := NewProviderLocal(ctx, nil, log.NewNOPFactory(), decoded.Tag, *local)
	require.NoError(t, err)
	instance := p.(*ProviderLocal)
	t.Cleanup(func() { cancel(); require.NoError(t, instance.Close()) })
	require.NoError(t, instance.StartContext(ctx, nil))
	return instance, ctx, manager, path
}

func TestLocalOverrideVMessHTTPHostOnWire(t *testing.T) {
	const nodes = `{"outbounds":[
		{"type":"vmess","tag":"山东 阿里云","server":"one.example","server_port":80,"uuid":"bf000d23-0752-40b4-affe-68f7707a9661","security":"auto","alter_id":0,"network":"tcp","transport":{"type":"http","host":["v.qq.com"],"path":"/","method":"GET"}},
		{"type":"vmess","tag":"广东 阿里云","server":"192.0.2.10","server_port":8080,"uuid":"bf000d23-0752-40b4-affe-68f7707a9661","security":"auto","alter_id":0,"transport":{"type":"http","host":["v.qq.com"],"path":"/","method":"GET"}},
		{"type":"vmess","tag":"青海 IPv6","server":"three.example","server_port":18552,"uuid":"bf000d23-0752-40b4-affe-68f7707a9661","security":"auto","alter_id":0,"transport":{"type":"http","host":["tms.dingtalk.com"],"path":"/","method":"GET"}}
	]}`
	_, ctx, manager, path := localOverrideProvider(t, nodes, `{"host":"m.youku.com"}`)
	for _, tag := range []string{"山东 阿里云", "广东 阿里云", "青海 IPv6"} {
		t.Run(tag, func(t *testing.T) {
			options := manager.option("免流节点/" + tag).(*option.VMessOutboundOptions)
			require.Equal(t, []string{"m.youku.com"}, []string(options.Transport.HTTPOptions.Host))
			// Keep the parsed node and transport unchanged, except for directing
			// its server connection to a local capture listener.
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			defer listener.Close()
			target := *options
			target.Server = "127.0.0.1"
			target.ServerPort = uint16(listener.Addr().(*net.TCPAddr).Port)
			ob, err := vmess.NewOutbound(ctx, nil, log.NewNOPFactory().NewLogger("test"), tag, target)
			require.NoError(t, err)
			conn, err := ob.DialContext(ctx, "tcp", M.ParseSocksaddr("example.com:443"))
			require.NoError(t, err)
			defer conn.Close()
			server, err := listener.Accept()
			require.NoError(t, err)
			defer server.Close()
			require.NoError(t, server.SetDeadline(time.Now().Add(5*time.Second)))
			written := make(chan error, 1)
			go func() { _, err := conn.Write([]byte("x")); written <- err }()
			req, err := http.ReadRequest(bufio.NewReader(server))
			require.NoError(t, err)
			require.Equal(t, "m.youku.com", req.Host)
			require.Equal(t, "GET", req.Method)
			require.Equal(t, "/", req.URL.Path)
			require.NoError(t, <-written)
		})
	}
	onDisk, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, nodes, string(onDisk), "overrides must not rewrite the user's source file")
}

func TestLocalHTTPConnectOverridesOnWire(t *testing.T) {
	for _, tt := range []struct{ name, override, target string }{
		{"path", `{"host":"m.youku.com","path":"@path.example"}`, "example.com:443@path.example"},
		{"with_at", `{"host":"m.youku.com","with_at":"at.example"}`, "example.com:443@at.example"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			defer listener.Close()
			port := listener.Addr().(*net.TCPAddr).Port
			nodes := fmt.Sprintf(`{"outbounds":[{"type":"http","tag":"node","server":"127.0.0.1","server_port":%d}]}`, port)
			_, ctx, manager, _ := localOverrideProvider(t, nodes, tt.override)
			options := manager.option("免流节点/node").(*option.HTTPOutboundOptions)
			ob, err := httpout.NewOutbound(ctx, nil, log.NewNOPFactory().NewLogger("test"), "node", *options)
			require.NoError(t, err)
			request := make(chan string, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					request <- err.Error()
					return
				}
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				reader := bufio.NewReader(conn)
				var content strings.Builder
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						request <- err.Error()
						return
					}
					content.WriteString(line)
					if line == "\r\n" {
						break
					}
				}
				request <- content.String()
				io.WriteString(conn, "HTTP/1.1 200 Connection established\r\n\r\n")
			}()
			conn, err := ob.DialContext(ctx, "tcp", M.ParseSocksaddr("example.com:443"))
			require.NoError(t, err)
			conn.Close()
			wire := <-request
			require.True(t, strings.HasPrefix(wire, "CONNECT "+tt.target+" HTTP/1.1\r\n"), wire)
			require.Contains(t, wire, "Host: m.youku.com\r\n")
			require.NotContains(t, wire, "With-At:")
		})
	}
}

func TestLocalVMessHostOverrideReload(t *testing.T) {
	node := func(host string) string {
		return fmt.Sprintf(`{"outbounds":[{"type":"vmess","tag":"node","server":"example.com","server_port":80,"uuid":"bf000d23-0752-40b4-affe-68f7707a9661","security":"auto","transport":{"type":"http","host":[%q],"path":"/"}}]}`, host)
	}
	for _, tt := range []struct {
		name, override, first, second string
	}{
		{"override", `{"host":"m.youku.com"}`, "m.youku.com", "m.youku.com"},
		{"no override", `{}`, "v.qq.com", "tms.dingtalk.com"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			instance, _, manager, path := localOverrideProvider(t, node("v.qq.com"), tt.override)
			host := func() []string {
				return []string(manager.option("免流节点/node").(*option.VMessOutboundOptions).Transport.HTTPOptions.Host)
			}
			require.Equal(t, []string{tt.first}, host())
			require.NoError(t, os.WriteFile(path, []byte(node("tms.dingtalk.com")), 0o600))
			require.NoError(t, instance.reloadFile(instance.path))
			require.Equal(t, []string{tt.second}, host())
		})
	}
}
