package provider

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/adapter/outbound"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/provider/parser"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/filemanager"
	"github.com/stretchr/testify/require"
)

func TestLocalWritebackPreservesOtherContent(t *testing.T) {
	const source = `{"extra":{"large":12345678901234567890},"outbounds":[{"type":"direct","tag":"skip","extra":"keep"},{"type":"vmess","tag":"node","server":"example.com","server_port":80,"uuid":"bf000d23-0752-40b4-affe-68f7707a9661","security":"auto","transport":{"type":"http","host":["old.example"],"path":"/original"}}]}`
	instance, ctx, manager, path := localOverrideProvider(t, source, `{"host":"new.example"}`)
	read := func() []byte { value, err := os.ReadFile(path); require.NoError(t, err); return value }
	written := read()
	var document map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(written, &document))
	require.JSONEq(t, `{"large":12345678901234567890}`, string(document["extra"]))
	require.Contains(t, string(written), `12345678901234567890`)
	require.Contains(t, string(written), `"extra": "keep"`)
	require.Contains(t, string(written), `"skip"`)
	require.Contains(t, string(written), `"/original"`)
	require.Contains(t, string(written), `"new.example"`)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	require.NoError(t, instance.Update())
	require.Equal(t, written, read())
	after, err := os.Stat(path)
	require.NoError(t, err)
	require.True(t, os.SameFile(info, after), "unchanged override should not rewrite file on reload")
	require.NotNil(t, manager.option("免流节点/node"))
	_, _, err = parser.ParseBoxSubscription(ctx, string(written))
	require.NoError(t, err)
}

func TestLocalWritebackClearAndOrderedHeaders(t *testing.T) {
	const source = `{"outbounds":[{"type":"http","tag":"node","server":"example.com","server_port":80,"path":"@old.example","headers":{"Host":"old.example","Z-Header":"keep","A-Header":"keep","with-at":"old.example"}}]}`
	_, ctx, _, path := localOverrideProvider(t, source, `{"host":"new.example","path":"","with_at":"new-at.example"}`)
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(content), `"path"`)
	require.Contains(t, string(content), `"Z-Header"`)
	require.Less(t, strings.Index(string(content), `"With-At"`), strings.Index(string(content), `"A-Header"`))
	nodes, _, err := parser.ParseBoxSubscription(ctx, string(content))
	require.NoError(t, err)
	require.Len(t, nodes, 1)
}

func TestLocalNonJSONRemainsUnchanged(t *testing.T) {
	source := "vless://bf000d23-0752-40b4-affe-68f7707a9661@example.com:443?type=ws&host=old.example#node\n"
	_, _, manager, path := localOverrideProvider(t, source, `{"host":"new.example"}`)
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, source, string(content))
	require.NotNil(t, manager.option("免流节点/node"))
}

func TestLocalNoOverrideDoesNotRewriteFile(t *testing.T) {
	const source = `{"outbounds":[{"type":"http","tag":"node","server":"example.com","server_port":80}]}`
	_, _, _, path := localOverrideProvider(t, source, `{}`)
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, source, string(content))
}

func TestLocalWritebackRefusesConcurrentModification(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node.json")
	require.NoError(t, os.WriteFile(path, []byte("original"), 0o600))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("external edit"), 0o600))
	p := &ProviderLocal{ctx: context.Background()}
	err = p.replaceLocalFile(path, []byte("original"), info, []byte("updated"))
	require.ErrorContains(t, err, "changed during override writeback")
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "external edit", string(content))
}

func TestLocalWritebackWatcherReloadsExternalEdits(t *testing.T) {
	const node = `{"outbounds":[{"type":"http","tag":"node","server":"example.com","server_port":80,"headers":{"Host":"old.example"}}]}`
	instance, ctx, manager, path := localOverrideProvider(t, node, `{"host":"new.example"}`)
	require.NoError(t, os.WriteFile(path, []byte(strings.Replace(node, "old.example", "other.example", 1)), 0o600))
	require.Eventually(t, func() bool {
		content, err := os.ReadFile(path)
		return err == nil && strings.Contains(string(content), "new.example")
	}, 5*time.Second, 20*time.Millisecond)
	require.NoError(t, instance.Update())
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	_, _, err = parser.ParseBoxSubscription(ctx, string(content))
	require.NoError(t, err)
	require.NotNil(t, manager.option("免流节点/node"))
}

func TestLocalWritebackPreservesSymlinkAndOriginalDetour(t *testing.T) {
	base := t.TempDir()
	original := filepath.Join(base, "original.json")
	link := filepath.Join(base, "link.json")
	source := `{"outbounds":[{"type":"http","tag":"upstream","server":"example.com","server_port":80},{"type":"http","tag":"node","server":"example.com","server_port":81,"detour":"upstream","headers":{"Host":"old.example"}}]}`
	require.NoError(t, os.WriteFile(original, []byte(source), 0o640))
	if err := os.Symlink(original, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	ctx := context.Background()
	p := &ProviderLocal{ctx: ctx}
	info, err := os.Stat(link)
	require.NoError(t, err)
	require.NoError(t, p.replaceLocalFile(link, []byte(source), info, []byte(strings.Replace(source, "old.example", "new.example", 1))))
	linkInfo, err := os.Lstat(link)
	require.NoError(t, err)
	require.NotZero(t, linkInfo.Mode()&os.ModeSymlink)
	written, err := os.ReadFile(original)
	require.NoError(t, err)
	require.Contains(t, string(written), `"detour":"upstream"`)
	info, err = os.Stat(original)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o640), info.Mode().Perm())
}

func TestLocalWritebackDoesNotPersistParserDetourNormalization(t *testing.T) {
	source := `{"outbounds":[{"type":"http","tag":"upstream","server":"example.com","server_port":80},{"type":"http","tag":"node","server":"example.com","server_port":81,"detour":"upstream","headers":{"Host":"old.example"}}]}`
	_, _, _, path := localOverrideProvider(t, source, `{"host":"new.example"}`)
	written, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(written), `"detour": "upstream"`)
	require.NotContains(t, string(written), `"detour": "免流节点/upstream"`)
}

func TestLocalWritebackOverrideDetourIsIdempotent(t *testing.T) {
	const source = `{"outbounds":[{"type":"http","tag":"node","server":"example.com","server_port":80}]}`
	instance, _, _, path := localOverrideProvider(t, source, `{"detour":"external"}`)
	initial, err := os.Stat(path)
	require.NoError(t, err)
	written, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(written), `"detour": "external"`)
	for range 3 {
		require.NoError(t, instance.Update())
	}
	after, err := os.Stat(path)
	require.NoError(t, err)
	require.True(t, os.SameFile(initial, after), "reloading must not continually rewrite an already saved detour")
}

func TestLocalCommentedJSONRemainsUnchanged(t *testing.T) {
	const source = `{"outbounds":[{"type":"http","tag":"node","server":"example.com","server_port":80,"headers":{"Host":"old.example"}}], // keep this comment
"other":true}`
	_, _, manager, path := localOverrideProvider(t, source, `{"host":"new.example"}`)
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, source, string(content))
	require.NotNil(t, manager.option("免流节点/node"))
}

func TestUniqueJSONKeys(t *testing.T) {
	require.True(t, uniqueJSONKeys([]byte(`{"a":1,"nested":{"a":2}}`)))
	require.False(t, uniqueJSONKeys([]byte(`{"a":1,"nested":{"a":2,"a":3}}`)))
}

func TestLocalWritebackTLSAndEndpointOverrides(t *testing.T) {
	const source = `{"outbounds":[{"type":"vmess","tag":"node","server":"example.com","server_port":80,"uuid":"bf000d23-0752-40b4-affe-68f7707a9661","security":"auto","tls":{"enabled":true,"server_name":"old.example"}}],"endpoints":[{"type":"wireguard","tag":"wg","address":"10.0.0.1/32","private_key":"test-key"}]}`
	outRegistry := outbound.NewRegistry()
	outbound.Register[option.VMessOutboundOptions](outRegistry, C.TypeVMess, nil)
	epRegistry := endpoint.NewRegistry()
	endpoint.Register[option.WireGuardEndpointOptions](epRegistry, C.TypeWireGuard, nil)
	ctx := service.ContextWith[option.OutboundOptionsRegistry](context.Background(), outRegistry)
	ctx = service.ContextWith[option.EndpointOptionsRegistry](ctx, epRegistry)
	p := &ProviderLocal{ctx: ctx, overrideDialer: &option.OverrideDialerOptions{BindInterface: common.Ptr("example0")}, overrideTLS: &option.OverrideTLSOptions{ServerName: common.Ptr("new.example")}}
	nodes, endpoints, err := parser.ParseSubscription(ctx, source, p.overrideDialer, p.overrideTLS, nil, nil, "")
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "nodes.json")
	require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.NoError(t, p.writeOverrides(path, []byte(source), info, nodes, endpoints))
	written, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(written), `"server_name": "new.example"`)
	parsed, ep, err := parser.ParseBoxSubscription(ctx, string(written))
	require.NoError(t, err)
	require.Equal(t, "example0", parsed[0].Options.(*option.VMessOutboundOptions).BindInterface)
	require.Equal(t, "example0", ep[0].Options.(*option.WireGuardEndpointOptions).BindInterface)
}

type deniedRenameManager struct{ filemanager.Manager }

func (m *deniedRenameManager) Rename(_, _ string) error { return fs.ErrPermission }

func TestLocalWritebackFailureKeepsOriginal(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "source.json")
	source := []byte("original")
	require.NoError(t, os.WriteFile(path, source, 0o600))
	ctx := filemanager.WithDefault(context.Background(), base, "", os.Getuid(), os.Getgid())
	ctx = service.ContextWith[filemanager.Manager](ctx, &deniedRenameManager{Manager: service.FromContext[filemanager.Manager](ctx)})
	p := &ProviderLocal{ctx: ctx}
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.ErrorIs(t, p.replaceLocalFile(path, source, info, []byte("updated")), fs.ErrPermission)
	written, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, source, written)
	entries, err := os.ReadDir(base)
	require.NoError(t, err)
	require.Len(t, entries, 1, "temporary file must be removed after failure")
}
