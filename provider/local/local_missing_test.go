package provider

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/filemanager"
	"github.com/stretchr/testify/require"
)

const localNode = `{"outbounds":[{"type":"socks","tag":"node","server":"127.0.0.1","server_port":1080}]}`

type localTestOutboundManager struct {
	adapter.OutboundManager
	mu        sync.Mutex
	outbounds map[string]adapter.Outbound
}

func (m *localTestOutboundManager) Outbound(tag string) (adapter.Outbound, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out, ok := m.outbounds[tag]
	return out, ok
}

func (m *localTestOutboundManager) Create(_ context.Context, _ adapter.Router, _ log.ContextLogger, tag string, _ string, _ any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.outbounds[tag] = &stubOutbound{tag: tag}
	return nil
}

func (m *localTestOutboundManager) Remove(tag string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.outbounds, tag)
	return nil
}

type localTestEndpointManager struct{ adapter.EndpointManager }

func (*localTestEndpointManager) Get(string) (adapter.Endpoint, bool) { return nil, false }

func newLocalTestProvider(t *testing.T, base, path string) (*ProviderLocal, context.Context) {
	t.Helper()
	ctx := filemanager.WithDefault(context.Background(), base, "", os.Getuid(), os.Getgid())
	ctx = service.ContextWith[adapter.OutboundManager](ctx, &localTestOutboundManager{outbounds: make(map[string]adapter.Outbound)})
	ctx = service.ContextWith[adapter.EndpointManager](ctx, &localTestEndpointManager{})
	registry := outbound.NewRegistry()
	outbound.Register[option.SOCKSOutboundOptions](registry, C.TypeSOCKS, nil)
	ctx = service.ContextWith[option.OutboundOptionsRegistry](ctx, registry)
	created, err := NewProviderLocal(ctx, nil, log.NewNOPFactory(), "local", option.ProviderLocalOptions{Path: path})
	require.NoError(t, err)
	provider := created.(*ProviderLocal)
	t.Cleanup(func() { require.NoError(t, provider.Close()) })
	return provider, ctx
}

func waitLocalNode(t *testing.T, p *ProviderLocal) {
	t.Helper()
	require.Eventually(t, func() bool {
		outbounds := p.Outbounds()
		return len(outbounds) == 1 && outbounds[0].Tag() == "local/node"
	}, 5*time.Second, 20*time.Millisecond)
}

func TestMissingLocalFileStartsAndLoadsWhenCreated(t *testing.T) {
	base := t.TempDir()
	provider, ctx := newLocalTestProvider(t, base, "missing.json")
	updates := make(chan string, 10)
	provider.RegisterCallback(func(tag string) error { updates <- tag; return nil })
	require.NoError(t, provider.StartContext(ctx, nil))
	require.Empty(t, provider.Outbounds())
	require.True(t, provider.UpdatedAt().IsZero())
	require.Equal(t, "local", <-updates)
	_, err := os.Stat(filepath.Join(base, "missing.json"))
	require.True(t, errors.Is(err, fs.ErrNotExist), "startup must not create an empty subscription file")

	require.NoError(t, os.WriteFile(filepath.Join(base, "missing.json"), []byte(localNode), 0o600))
	waitLocalNode(t, provider)
	require.Eventually(t, func() bool { return len(updates) > 0 }, time.Second, 10*time.Millisecond)
	require.False(t, provider.UpdatedAt().IsZero())

	require.NoError(t, os.Remove(filepath.Join(base, "missing.json")))
	require.ErrorIs(t, provider.Update(), fs.ErrNotExist)
	require.Len(t, provider.Outbounds(), 1, "a failed update must keep the last valid nodes")
}

func TestMissingLocalParentStartsAndLoadsWhenCreated(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join("missing", "nested", "nodes.json")
	provider, ctx := newLocalTestProvider(t, base, path)
	require.NoError(t, provider.StartContext(ctx, nil))
	require.Empty(t, provider.Outbounds())
	_, err := os.Stat(filepath.Join(base, "missing"))
	require.ErrorIs(t, err, fs.ErrNotExist)
	require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(base, path)), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(base, path), []byte(localNode), 0o600))
	waitLocalNode(t, provider)
}

func TestLocalFileErrorsStillFailStartup(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(t *testing.T, path string)
	}{
		{"invalid subscription", func(t *testing.T, path string) {
			require.NoError(t, os.WriteFile(path, []byte("not a subscription"), 0o600))
		}},
		{"path is directory", func(t *testing.T, path string) { require.NoError(t, os.Mkdir(path, 0o700)) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := t.TempDir()
			test.setup(t, filepath.Join(base, "nodes"))
			provider, ctx := newLocalTestProvider(t, base, "nodes")
			require.Error(t, provider.StartContext(ctx, nil))
		})
	}
}

func TestLocalExistingSubscriptionStillLoads(t *testing.T) {
	base := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(base, "nodes.json"), []byte(localNode), 0o600))
	provider, ctx := newLocalTestProvider(t, base, "nodes.json")
	require.NoError(t, provider.StartContext(ctx, nil))
	require.Len(t, provider.Outbounds(), 1)
}

func TestMissingLocalParentWatchesFileCreatedLater(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join("missing", "nodes.json")
	provider, ctx := newLocalTestProvider(t, base, path)
	require.NoError(t, provider.StartContext(ctx, nil))
	require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(base, path)), 0o700))
	select {
	case <-provider.watchDone:
	case <-time.After(5 * time.Second):
		t.Fatal("watcher did not start after parent directory was created")
	}
	require.NoError(t, os.WriteFile(filepath.Join(base, path), []byte(localNode), 0o600))
	waitLocalNode(t, provider)
}

func TestLocalPathStillRequired(t *testing.T) {
	_, err := NewProviderLocal(context.Background(), nil, log.NewNOPFactory(), "local", option.ProviderLocalOptions{})
	require.ErrorContains(t, err, "provider path is required")
}

type deniedFileManager struct{ filemanager.Manager }

func (m *deniedFileManager) OpenFile(name string, _ int, _ os.FileMode) (*os.File, error) {
	return nil, &os.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
}

func TestLocalReadPermissionErrorStillFailsStartup(t *testing.T) {
	provider, ctx := newLocalTestProvider(t, t.TempDir(), "nodes.json")
	manager := service.FromContext[filemanager.Manager](ctx)
	ctx = service.ContextWith[filemanager.Manager](ctx, &deniedFileManager{Manager: manager})
	provider.ctx = ctx
	require.ErrorIs(t, provider.StartContext(ctx, nil), fs.ErrPermission)
}

func TestCloseWhileWaitingForLocalParent(t *testing.T) {
	provider, ctx := newLocalTestProvider(t, t.TempDir(), filepath.Join("missing", "nodes.json"))
	require.NoError(t, provider.StartContext(ctx, nil))
	closed := make(chan error, 1)
	go func() { closed <- provider.Close() }()
	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("close blocked while waiting for the missing directory")
	}
}
