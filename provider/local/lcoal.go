package provider

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sagernet/fswatch"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/provider"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/provider/parser"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	F "github.com/sagernet/sing/common/format"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/filemanager"
)

func RegisterProviderLocal(registry *provider.Registry) {
	provider.Register[option.ProviderLocalOptions](registry, C.ProviderTypeLocal, NewProviderLocal)
}

func RegisterProviderInline(registry *provider.Registry) {
	provider.Register[option.ProviderInlineOptions](registry, C.ProviderTypeInline, NewProviderInline)
}

var (
	_ adapter.Provider        = (*ProviderLocal)(nil)
	_ adapter.ProviderUpdater = (*ProviderLocal)(nil)
)

type ProviderLocal struct {
	provider.Adapter
	ctx         context.Context
	logger      log.ContextLogger
	provider    adapter.ProviderManager
	path        string
	mu          sync.Mutex
	lastOutOpts []option.Outbound
	lastEPOpts  []option.Endpoint
	lastUpdated time.Time
	watcher     *fswatch.Watcher
	watchCancel context.CancelFunc
	watchDone   chan struct{}

	overrideDialer    *option.OverrideDialerOptions
	overrideTLS       *option.OverrideTLSOptions
	overrideAnyTLS    *option.OverrideAnyTLSOptions
	overrideTransport *option.OverrideTransportOptions
}

func NewProviderInline(ctx context.Context, router adapter.Router, logFactory log.Factory, tag string, options option.ProviderInlineOptions) (adapter.Provider, error) {
	var (
		outbound    = service.FromContext[adapter.OutboundManager](ctx)
		endpointMgr = service.FromContext[adapter.EndpointManager](ctx)
		logger      = logFactory.NewLogger(F.ToString("provider/inline", "[", tag, "]"))
	)
	provider := &ProviderLocal{
		Adapter: provider.NewAdapter(ctx, router, outbound, endpointMgr, logFactory, logger, tag, C.ProviderTypeInline, options.HealthCheck),
		ctx:     ctx,
		logger:  logger,
	}
	provider.RewriteDetourForProvider(options.Outbounds)
	provider.UpdateOutbounds(nil, options.Outbounds)
	if len(options.Endpoints) > 0 {
		provider.RewriteDetourForProviderEndpoints(options.Endpoints)
		provider.UpdateEndpoints(nil, options.Endpoints)
	}
	return provider, nil
}

func NewProviderLocal(ctx context.Context, router adapter.Router, logFactory log.Factory, tag string, options option.ProviderLocalOptions) (adapter.Provider, error) {
	if options.Path == "" {
		return nil, E.New("provider path is required")
	}
	var (
		outbound    = service.FromContext[adapter.OutboundManager](ctx)
		endpointMgr = service.FromContext[adapter.EndpointManager](ctx)
		logger      = logFactory.NewLogger(F.ToString("provider/local", "[", tag, "]"))
	)
	provider := &ProviderLocal{
		Adapter:  provider.NewAdapter(ctx, router, outbound, endpointMgr, logFactory, logger, tag, C.ProviderTypeLocal, options.HealthCheck),
		ctx:      ctx,
		logger:   logger,
		provider: service.FromContext[adapter.ProviderManager](ctx),

		overrideDialer:    options.OverrideDialer,
		overrideTLS:       options.OverrideTLS,
		overrideAnyTLS:    options.OverrideAnyTLS,
		overrideTransport: options.OverrideTransport,
	}
	filePath := filemanager.BasePath(ctx, options.Path)
	absPath, _ := filepath.Abs(filePath)
	provider.path = absPath

	dir := filepath.Dir(absPath)
	targetFile := filepath.Base(absPath)

	watcher, err := fswatch.NewWatcher(fswatch.Options{
		Path: []string{absPath, dir},
		Callback: func(eventPath string) {
			if filepath.Base(eventPath) != targetFile && eventPath != absPath {
				return
			}
			provider.reloadWatchedFile()
		},
	})
	if err != nil {
		return nil, err
	}
	provider.watcher = watcher
	return provider, nil
}

func (s *ProviderLocal) StartContext(ctx context.Context, startContext *adapter.HTTPStartContext) error {
	if s.path == "" {
		// Inline outbounds are created in the constructor, before any group
		// has registered its callback, so groups must be told about them here.
		s.UpdateGroups()
	} else {
		err := s.reloadFile(s.path)
		missing := errors.Is(err, fs.ErrNotExist)
		if err != nil && !missing {
			return err
		}
		if missing {
			s.logger.Warn("local provider file not found, starting empty: ", s.path)
		}
		s.UpdateGroups()
		if s.watcher != nil {
			if err = s.startWatcher(); err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					s.waitForWatchDirectory()
				} else {
					s.logger.Error(E.Cause(err, "watch provider file"))
				}
			} else if missing {
				// Close the gap between the first read and installing the watcher.
				s.reloadWatchedFile()
			}
		}
	}
	return s.Adapter.Start()
}

// startWatcher checks the directory first so a missing parent does not create
// a leaking fsnotify watcher. A missing directory is retried in the background.
func (s *ProviderLocal) startWatcher() error {
	_, err := os.Stat(filepath.Dir(s.path))
	if err != nil {
		return err
	}
	return s.watcher.Start()
}

func (s *ProviderLocal) waitForWatchDirectory() {
	ctx, cancel := context.WithCancel(s.ctx)
	s.watchCancel = cancel
	s.watchDone = make(chan struct{})
	go func() {
		defer close(s.watchDone)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.startWatcher(); err != nil {
					if !errors.Is(err, fs.ErrNotExist) {
						s.logger.Error(E.Cause(err, "watch provider file"))
						return
					}
					continue
				}
				// The file may already exist by the time its parent is watched.
				s.reloadWatchedFile()
				return
			}
		}
	}()
}

func (s *ProviderLocal) reloadWatchedFile() {
	err := s.reloadFile(s.path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.logger.Error(E.Cause(err, "reload provider ", s.Tag()))
		}
		return
	}
	s.logger.Info("reloaded local provider ", s.Tag())
	s.UpdateGroups()
}

func (s *ProviderLocal) Update() error {
	if s.path == "" {
		return nil
	}
	err := s.reloadFile(s.path)
	if err != nil {
		return err
	}
	s.UpdateGroups()
	return nil
}

func (s *ProviderLocal) UpdatedAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastUpdated
}

func (s *ProviderLocal) reloadFile(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := filemanager.Open(s.ctx, path)
	if err != nil {
		return err
	}
	content, err := io.ReadAll(file)
	if err != nil {
		file.Close()
		return err
	}
	fileInfo, err := file.Stat()
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	s.lastUpdated = fileInfo.ModTime()
	outboundOpts, endpointOpts, err := parser.ParseSubscription(parser.ContextWithWarnHandler(s.ctx, s.logger.Warn), string(content), s.overrideDialer, s.overrideTLS, s.overrideAnyTLS, s.overrideTransport, s.Tag())
	if err != nil {
		return err
	}
	s.UpdateOutbounds(s.lastOutOpts, outboundOpts)
	s.lastOutOpts = outboundOpts
	s.UpdateEndpoints(s.lastEPOpts, endpointOpts)
	s.lastEPOpts = endpointOpts
	return nil
}

func (s *ProviderLocal) Close() error {
	if s.watchCancel != nil {
		s.watchCancel()
		<-s.watchDone
	}
	return common.Close(common.PtrOrNil(s.watcher), &s.Adapter)
}
