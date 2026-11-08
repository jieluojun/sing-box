package provider

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	httpout "github.com/sagernet/sing-box/protocol/http"
	"github.com/sagernet/sing-box/protocol/vmess"
	"github.com/sagernet/sing-box/provider/parser"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestLocalClashYAMLVMessOverrideWritesAndSendsHost(t *testing.T) {
	const source = `# provider comment
proxies:
  - {name: 山东 阿里云, type: vmess, server: one.example, port: 80, cipher: auto, uuid: bf000d23-0752-40b4-affe-68f7707a9661, alterId: 0, tls: false, network: http, http-opts: {path: [/], headers: {Host: [v.qq.com]}, method: GET}}
  - {name: 广东 阿里云, type: vmess, server: 192.0.2.10, port: 8080, uuid: bf000d23-0752-40b4-affe-68f7707a9661, alterId: 0, cipher: auto, udp: true, network: http, http-opts: {path: [/], headers: {Host: [v.qq.com]}}, tls: false}
  - {name: 青海 IPv6, type: vmess, server: three.example, port: 18552, uuid: bf000d23-0752-40b4-affe-68f7707a9661, alterId: 0, cipher: auto, udp: true, network: http, http-opts: {method: GET, path: [/], headers: {Host: [tms.dingtalk.com]}}}
`
	instance, ctx, manager, path := localOverrideProvider(t, source, `{"host":"m.youku.com","path":"@connect.example","with_at":"at.example"}`)
	written, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(written), "# provider comment")
	require.Equal(t, 3, strings.Count(string(written), "m.youku.com"))
	require.NotContains(t, string(written), "@connect.example")
	require.NotContains(t, string(written), "at.example")
	var doc yaml.Node
	require.NoError(t, yaml.Unmarshal(written, &doc))
	proxies, _ := yamlField(doc.Content[0], "proxies", false)
	require.Len(t, proxies.Content, 3)
	for _, node := range proxies.Content {
		require.NotZero(t, node.Style&yaml.FlowStyle)
		opts, _ := yamlField(node, "http-opts", false)
		headers, _ := yamlField(opts, "headers", false)
		host, _ := yamlField(headers, "Host", true)
		require.Equal(t, yaml.SequenceNode, host.Kind)
		require.Len(t, host.Content, 1)
		require.Equal(t, "m.youku.com", host.Content[0].Value)
		originalPath, _ := yamlField(opts, "path", false)
		require.Equal(t, "/", originalPath.Content[0].Value)
	}
	saved, _, err := parser.ParseSubscription(ctx, string(written), nil, nil, nil, nil, "免流节点")
	require.NoError(t, err)
	for _, out := range saved {
		opts := out.Options.(*option.VMessOutboundOptions)
		require.Equal(t, []string{"m.youku.com"}, []string(opts.Transport.HTTPOptions.Host))
		require.Empty(t, opts.Transport.HTTPOptions.Headers.Build().Get("Host"))
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		target := *opts
		target.Server = "127.0.0.1"
		target.ServerPort = uint16(listener.Addr().(*net.TCPAddr).Port)
		ob, err := vmess.NewOutbound(ctx, nil, log.NewNOPFactory().NewLogger("test"), out.Tag, target)
		require.NoError(t, err)
		conn, err := ob.DialContext(ctx, "tcp", M.ParseSocksaddr("example.com:443"))
		require.NoError(t, err)
		server, err := listener.Accept()
		require.NoError(t, err)
		require.NoError(t, server.SetDeadline(time.Now().Add(5*time.Second)))
		writtenResult := make(chan error, 1)
		go func() { _, err := conn.Write([]byte("x")); writtenResult <- err }()
		req, err := http.ReadRequest(bufio.NewReader(server))
		require.NoError(t, err)
		require.Equal(t, "m.youku.com", req.Host)
		require.Equal(t, "/", req.URL.Path)
		require.NoError(t, <-writtenResult)
		conn.Close()
		server.Close()
		listener.Close()
	}
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.NoError(t, instance.Update())
	after, err := os.Stat(path)
	require.NoError(t, err)
	require.True(t, os.SameFile(info, after), "reload must not rewrite an already overridden source")
	require.NotNil(t, manager.option("免流节点/山东 阿里云"))
}

func TestLocalClashYAMLHTTPConnectOverrides(t *testing.T) {
	for _, tt := range []struct{ name, override, target string }{
		{"path", `{"host":"m.youku.com","path":"@path.example"}`, "example.com:443@path.example"},
		{"with_at", `{"host":"m.youku.com","with_at":"at.example"}`, "example.com:443@at.example"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			defer listener.Close()
			port := listener.Addr().(*net.TCPAddr).Port
			source := fmt.Sprintf("proxies:\n  - {name: node, type: http, server: 127.0.0.1, port: %d, headers: {X-Test: keep, Host: old.example}}\n", port)
			_, ctx, _, path := localOverrideProvider(t, source, tt.override)
			written, err := os.ReadFile(path)
			require.NoError(t, err)
			saved, _, err := parser.ParseSubscription(ctx, string(written), nil, nil, nil, nil, "免流节点")
			require.NoError(t, err)
			opts := saved[0].Options.(*option.HTTPOutboundOptions)
			require.Equal(t, "m.youku.com", opts.Headers.Build().Get("Host"))
			if tt.name == "path" {
				require.Equal(t, "@path.example", opts.Path)
			} else {
				require.Equal(t, "at.example", opts.Headers.Build().Get("With-At"))
			}
			ob, err := httpout.NewOutbound(ctx, nil, log.NewNOPFactory().NewLogger("test"), "node", *opts)
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

func TestLocalClashYAMLHostOtherTransportsAndUnsupportedNodes(t *testing.T) {
	const source = `# keep header
proxies:
  - {name: h2, type: vmess, server: example.com, port: 443, uuid: bf000d23-0752-40b4-affe-68f7707a9661, cipher: auto, network: h2, h2-opts: {path: /keep, host: [old.example, second.example]}}
  - {name: ws, type: vless, server: example.com, port: 443, uuid: bf000d23-0752-40b4-affe-68f7707a9661, network: ws, ws-opts: {path: /keep, headers: {X-Test: keep, host: old.example}}}
  - {name: upgrade, type: trojan, server: example.com, port: 443, password: test-password, network: ws, ws-opts: {v2ray-http-upgrade: true, headers: {host: old.example}}}
  - {name: thttp, type: trojan, server: example.com, port: 443, password: test-password, network: http, http-opts: {path: [/keep], headers: {Host: [old.example]}}}
  - {name: xhttp, type: vless, server: example.com, port: 443, uuid: bf000d23-0752-40b4-affe-68f7707a9661, network: xhttp, xhttp-opts: {path: /keep, headers: {Host: old.example, X-Test: keep}, download-settings: {host: download.example}}}
  - {name: grpc, type: vmess, server: example.com, port: 443, uuid: bf000d23-0752-40b4-affe-68f7707a9661, cipher: auto, network: grpc, grpc-opts: {grpc-service-name: keep}}
  - {name: unknown, type: ssr, server: secret.example, port: 80, extra: keep}
other: {z: 1, a: 2}
`
	instance, ctx, _, path := localOverrideProvider(t, source, `{"host":"new.example"}`)
	written, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(written), "# keep header")
	require.Contains(t, string(written), "download.example")
	require.Contains(t, string(written), "secret.example")
	require.Contains(t, string(written), "grpc-service-name: keep")
	var document yaml.Node
	require.NoError(t, yaml.Unmarshal(written, &document))
	proxies, _ := yamlField(document.Content[0], "proxies", false)
	require.Len(t, proxies.Content, 7)
	// Existing keys retain their order, and a newly introduced host is appended.
	require.Equal(t, []string{"name", "type", "server", "port", "uuid", "cipher", "network", "h2-opts"}, yamlKeys(proxies.Content[0]))
	xhttp, _ := yamlField(proxies.Content[4], "xhttp-opts", false)
	require.Equal(t, []string{"path", "headers", "download-settings", "host"}, yamlKeys(xhttp))
	parsed, _, err := parser.ParseSubscription(ctx, string(written), nil, nil, nil, nil, "免流节点")
	require.NoError(t, err)
	require.Len(t, parsed, 6)
	require.Equal(t, []string{"new.example"}, []string(parsed[0].Options.(*option.VMessOutboundOptions).Transport.HTTPOptions.Host))
	require.Equal(t, "new.example", parsed[1].Options.(*option.VLESSOutboundOptions).Transport.WebsocketOptions.Headers.Build().Get("Host"))
	require.Equal(t, "new.example", parsed[2].Options.(*option.TrojanOutboundOptions).Transport.HTTPUpgradeOptions.Host)
	require.Equal(t, []string{"new.example"}, []string(parsed[3].Options.(*option.TrojanOutboundOptions).Transport.HTTPOptions.Host))
	x := parsed[4].Options.(*option.VLESSOutboundOptions).Transport.Extra.(*option.V2RayXHTTPOptions)
	require.Equal(t, []string{"new.example"}, []string(x.Host))
	require.Equal(t, "keep", x.Headers.Build().Get("X-Test"))
	require.Equal(t, []string{"download.example"}, []string(x.DownloadSettings.Host))
	initial, err := os.Stat(path)
	require.NoError(t, err)
	require.NoError(t, instance.Update())
	after, err := os.Stat(path)
	require.NoError(t, err)
	require.True(t, os.SameFile(initial, after))
}

func yamlKeys(mapping *yaml.Node) []string {
	var keys []string
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		keys = append(keys, mapping.Content[i].Value)
	}
	return keys
}

func TestLocalClashYAMLHTTPHeaderOrderAndClearing(t *testing.T) {
	const source = `proxies:
  - {name: node, type: http, server: example.com, port: 80, path: "@old.example", headers: {Z: z, host: old.example, HOST: stale.example, With-At: old.example, with-at: stale.example, A: a}}
`
	for _, tt := range []struct {
		name, override string
		headers        []string
		path           bool
	}{
		{"host and with_at", `{"host":"new.example","with_at":"new-at.example"}`, []string{"With-At", "Z", "host", "A"}, true},
		{"clear", `{"path":"","with_at":""}`, []string{"Z", "host", "HOST", "A"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, ctx, _, path := localOverrideProvider(t, source, tt.override)
			written, err := os.ReadFile(path)
			require.NoError(t, err)
			var doc yaml.Node
			require.NoError(t, yaml.Unmarshal(written, &doc))
			proxies, _ := yamlField(doc.Content[0], "proxies", false)
			proxy := proxies.Content[0]
			headers, _ := yamlField(proxy, "headers", false)
			require.Equal(t, tt.headers, yamlKeys(headers))
			_, index := yamlField(proxy, "path", false)
			require.Equal(t, tt.path, index >= 0)
			saved, _, err := parser.ParseSubscription(ctx, string(written), nil, nil, nil, nil, "免流节点")
			require.NoError(t, err)
			opts := saved[0].Options.(*option.HTTPOutboundOptions)
			if tt.name == "host and with_at" {
				require.Equal(t, "new.example", opts.Headers.Build().Get("Host"))
				require.Equal(t, "new-at.example", opts.Headers.Build().Get("With-At"))
			} else {
				require.Empty(t, opts.Path)
				require.Empty(t, opts.Headers.Build().Get("With-At"))
			}
		})
	}
}

func TestLocalClashYAMLSafetySkipsUnsupportedRewrites(t *testing.T) {
	for _, tt := range []struct{ name, source string }{
		{"anchors", "common: &headers {Host: [old.example]}\nproxies:\n  - {name: node, type: vmess, server: example.com, port: 80, uuid: bf000d23-0752-40b4-affe-68f7707a9661, cipher: auto, network: http, http-opts: {headers: *headers}}\n"},
		{"multiple documents", "proxies:\n  - {name: node, type: vmess, server: example.com, port: 80, uuid: bf000d23-0752-40b4-affe-68f7707a9661, cipher: auto, network: http, http-opts: {headers: {Host: [old.example]}}}\n---\nother: keep\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, _, manager, path := localOverrideProvider(t, tt.source, `{"host":"new.example"}`)
			written, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, tt.source, string(written))
			require.Equal(t, []string{"new.example"}, []string(manager.option("免流节点/node").(*option.VMessOutboundOptions).Transport.HTTPOptions.Host))
		})
	}
}

func TestLocalClashYAMLXHTTPKeepsInheritedDownloadHost(t *testing.T) {
	for _, tt := range []struct {
		name, upload string
		wantDownload []string
	}{
		{"inherited host", "host: old.example, ", []string{"old.example"}},
		{"no inherited host", "", nil},
		{"host inherited from header", "headers: {Host: old.example, X-Test: keep}, ", []string{"old.example"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			source := fmt.Sprintf("proxies:\n  - {name: node, type: vless, server: example.com, port: 443, uuid: bf000d23-0752-40b4-affe-68f7707a9661, network: xhttp, xhttp-opts: {%sdownload-settings: {path: /down}}}\n", tt.upload)
			_, ctx, _, path := localOverrideProvider(t, source, `{"host":"new.example"}`)
			written, err := os.ReadFile(path)
			require.NoError(t, err)
			parsed, _, err := parser.ParseSubscription(ctx, string(written), nil, nil, nil, nil, "免流节点")
			require.NoError(t, err)
			x := parsed[0].Options.(*option.VLESSOutboundOptions).Transport.Extra.(*option.V2RayXHTTPOptions)
			require.Equal(t, []string{"new.example"}, []string(x.Host))
			require.Equal(t, tt.wantDownload, []string(x.DownloadSettings.Host))
			require.Equal(t, "/down", x.DownloadSettings.Path)
		})
	}
}

func TestLocalClashYAMLAddsMissingTransportAndHeaders(t *testing.T) {
	const source = `proxies:
  - {name: http, type: vmess, server: example.com, port: 80, uuid: bf000d23-0752-40b4-affe-68f7707a9661, cipher: auto, network: http}
  - {name: h2, type: vless, server: example.com, port: 443, uuid: bf000d23-0752-40b4-affe-68f7707a9661, network: h2}
  - {name: ws, type: trojan, server: example.com, port: 443, password: test, network: ws}
  - {name: connect, type: http, server: example.com, port: 8080}
`
	_, ctx, _, path := localOverrideProvider(t, source, `{"host":"new.example","with_at":"at.example"}`)
	written, err := os.ReadFile(path)
	require.NoError(t, err)
	saved, _, err := parser.ParseSubscription(ctx, string(written), nil, nil, nil, nil, "免流节点")
	require.NoError(t, err)
	require.Equal(t, []string{"new.example"}, []string(saved[0].Options.(*option.VMessOutboundOptions).Transport.HTTPOptions.Host))
	require.Equal(t, []string{"new.example"}, []string(saved[1].Options.(*option.VLESSOutboundOptions).Transport.HTTPOptions.Host))
	require.Equal(t, "new.example", saved[2].Options.(*option.TrojanOutboundOptions).Transport.WebsocketOptions.Headers.Build().Get("Host"))
	headers := saved[3].Options.(*option.HTTPOutboundOptions).Headers.Build()
	require.Equal(t, "new.example", headers.Get("Host"))
	require.Equal(t, "at.example", headers.Get("With-At"))
}

func TestLocalClashYAMLHostOnlyKeepsWithAtFirst(t *testing.T) {
	source := "proxies:\n  - {name: node, type: http, server: example.com, port: 80, headers: {Z: z, Host: old.example, With-At: at.example, A: a}}\n"
	_, _, _, path := localOverrideProvider(t, source, `{"host":"new.example"}`)
	written, err := os.ReadFile(path)
	require.NoError(t, err)
	var doc yaml.Node
	require.NoError(t, yaml.Unmarshal(written, &doc))
	proxies, _ := yamlField(doc.Content[0], "proxies", false)
	headers, _ := yamlField(proxies.Content[0], "headers", false)
	require.Equal(t, []string{"With-At", "Z", "Host", "A"}, yamlKeys(headers))
}

func TestLocalClashYAMLWatchReload(t *testing.T) {
	const source = "proxies:\n  - {name: node, type: vmess, server: example.com, port: 80, uuid: bf000d23-0752-40b4-affe-68f7707a9661, cipher: auto, network: http, http-opts: {headers: {Host: [old.example]}}}\n"
	_, _, manager, path := localOverrideProvider(t, source, `{"host":"new.example"}`)
	require.NoError(t, os.WriteFile(path, []byte(strings.Replace(source, "old.example", "external.example", 1)), 0o600))
	require.Eventually(t, func() bool {
		written, err := os.ReadFile(path)
		return err == nil && strings.Contains(string(written), "new.example")
	}, 5*time.Second, 20*time.Millisecond)
	require.Equal(t, []string{"new.example"}, []string(manager.option("免流节点/node").(*option.VMessOutboundOptions).Transport.HTTPOptions.Host))
}

func TestLocalClashYAMLKeepsQuotedValuesAndComments(t *testing.T) {
	const source = `# top comment
proxies:
  - name: node # node comment
    type: http
    server: example.com
    port: 80
    headers:
      Z-Test: keep # other header comment
      Host: 'old.example' # host comment
`
	_, _, _, path := localOverrideProvider(t, source, `{"host":"new.example"}`)
	written, err := os.ReadFile(path)
	require.NoError(t, err)
	text := string(written)
	for _, kept := range []string{"# top comment", "# node comment", "# other header comment", "# host comment", "'new.example'"} {
		require.Contains(t, text, kept)
	}
}
