package urltest

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/sagernet/sing-anytls"
	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-mux"
	"github.com/sagernet/sing-snell"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/ntp"
	"github.com/sagernet/sing/common/observable"
)

// DefaultLink is probed when no URL is configured.
const DefaultLink = "https://www.gstatic.com/generate_204"

type unifiedDelayKey struct{}

// ContextWithUnifiedDelay binds the measurement policy to one instance or request.
func ContextWithUnifiedDelay(ctx context.Context, enabled bool) context.Context {
	return context.WithValue(ctx, unifiedDelayKey{}, enabled)
}

func UnifiedDelayFromContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	enabled, _ := ctx.Value(unifiedDelayKey{}).(bool)
	return enabled
}

// ════════════════ HistoryStorage ════════════════

var (
	_ adapter.URLTestHistoryStorage = (*HistoryStorage)(nil)
	_ adapter.UDPTestHistoryStorage = (*HistoryStorage)(nil)
)

type HistoryStorage struct {
	delayHistory sync.Map
	udpHistory   sync.Map
	updateHooks  []*observable.Subscriber[struct{}]
	hookAccess   sync.Mutex

	notifyAccess  sync.Mutex
	notifyPending bool
	lastNotify    time.Time

	prober Prober
}

// historyNotifyInterval coalesces store/delete notifications: a health
// check stores one result per member, and every notification makes the
// subscribers (reference tracking, dashboard pushes) walk all outbounds.
const historyNotifyInterval = 200 * time.Millisecond

func NewHistoryStorage() *HistoryStorage { return &HistoryStorage{} }

// Prober shares probes between everything measuring through this storage.
func (s *HistoryStorage) Prober() *Prober {
	return &s.prober
}

func (s *HistoryStorage) AddUpdateHook(hook *observable.Subscriber[struct{}]) {
	s.hookAccess.Lock()
	defer s.hookAccess.Unlock()
	s.updateHooks = append(s.updateHooks, hook)
}

func (s *HistoryStorage) NotifyUpdated() {
	s.hookAccess.Lock()
	hooks := s.updateHooks
	s.hookAccess.Unlock()
	for _, h := range hooks {
		h.Emit(struct{}{})
	}
}

func (s *HistoryStorage) LoadURLTestHistory(tag string) *adapter.URLTestHistory {
	if s == nil {
		return nil
	}
	v, ok := s.delayHistory.Load(tag)
	if !ok {
		return nil
	}
	return v.(*adapter.URLTestHistory)
}

func (s *HistoryStorage) DeleteURLTestHistory(tag string) {
	s.delayHistory.Delete(tag)
	s.notifyUpdated()
}

func (s *HistoryStorage) StoreURLTestHistory(tag string, h *adapter.URLTestHistory) {
	s.delayHistory.Store(tag, h)
	s.notifyUpdated()
}

func (s *HistoryStorage) LoadUDPTestHistory(tag string) *adapter.UDPTestHistory {
	if s == nil {
		return nil
	}
	v, ok := s.udpHistory.Load(tag)
	if !ok {
		return nil
	}
	return v.(*adapter.UDPTestHistory)
}

func (s *HistoryStorage) StoreUDPTestHistory(tag string, h *adapter.UDPTestHistory) {
	s.udpHistory.Store(tag, h)
	s.notifyUpdated()
}

func (s *HistoryStorage) DeleteUDPTestHistory(tag string) {
	s.udpHistory.Delete(tag)
	s.notifyUpdated()
}

// notifyUpdated emits right away when the last emission is older than
// historyNotifyInterval, and otherwise schedules a single trailing emission.
func (s *HistoryStorage) notifyUpdated() {
	s.notifyAccess.Lock()
	if s.notifyPending {
		s.notifyAccess.Unlock()
		return
	}
	wait := historyNotifyInterval - time.Since(s.lastNotify)
	if wait <= 0 {
		s.lastNotify = time.Now()
		s.notifyAccess.Unlock()
		s.NotifyUpdated()
		return
	}
	s.notifyPending = true
	s.notifyAccess.Unlock()
	time.AfterFunc(wait, func() {
		s.notifyAccess.Lock()
		s.notifyPending = false
		s.lastNotify = time.Now()
		s.notifyAccess.Unlock()
		s.NotifyUpdated()
	})
}

func (s *HistoryStorage) Close() error {
	s.hookAccess.Lock()
	s.updateHooks = nil
	s.hookAccess.Unlock()
	return nil
}

// ════════════════ Probe ════════════════

// URLTest measures link through detour.
func URLTest(ctx context.Context, link string, detour N.Dialer) (uint16, error) {
	return URLTestWithStatus(ctx, link, detour, nil)
}

// URLTestWithStatus measures link through detour and, when matcher is set,
// rejects responses whose status code it does not accept.
//
// Multiplexed outbounds open their session with a warm-up request first, so
// the measurement covers a new stream on an established session like real
// connections see.
func URLTestWithStatus(ctx context.Context, link string, detour N.Dialer, matcher *StatusMatcher) (uint16, error) {
	multiplexOutbound, isMultiplexOutbound := common.Cast[adapter.OutboundWithMultiplex](detour)
	if isMultiplexOutbound && multiplexOutbound.MultiplexEnabled() {
		_, err := urlTest(contextWithKeepSession(ctx), link, detour, matcher)
		if err != nil {
			return 0, err
		}
	}
	return urlTest(ctx, link, detour, matcher)
}

func contextWithKeepSession(ctx context.Context) context.Context {
	ctx = adapter.ContextWithKeepSession(ctx)
	ctx = mux.ContextWithKeepSession(ctx)
	ctx = anytls.ContextWithKeepSession(ctx)
	ctx = contextWithQUICKeepSession(ctx)
	return snell.ContextWithKeepSession(ctx)
}

func urlTest(ctx context.Context, link string, detour N.Dialer, matcher *StatusMatcher) (t uint16, err error) {
	if link == "" {
		link = DefaultLink
	}
	linkURL, err := url.Parse(link)
	if err != nil {
		return
	}
	hostname := linkURL.Hostname()
	port := linkURL.Port()
	if port == "" {
		switch linkURL.Scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		}
	}

	start := time.Now()
	instance, err := detour.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddrHostPortStr(hostname, port))
	if err != nil {
		return
	}
	defer instance.Close()
	if N.NeedHandshakeForWrite(instance) {
		start = time.Now()
	}
	req, err := http.NewRequest(http.MethodHead, link, nil)
	if err != nil {
		return
	}
	client := http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return instance, nil
			},
			TLSClientConfig: &tls.Config{
				Time:    ntp.TimeFuncFromContext(ctx),
				RootCAs: adapter.RootPoolFromContext(ctx),
			},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: C.TCPTimeout,
	}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		return
	}
	resp.Body.Close()
	if err = validateStatus(resp.StatusCode, matcher); err != nil {
		return
	}
	elapsed := time.Since(start)
	if UnifiedDelayFromContext(ctx) {
		second := time.Now()
		secondResp, secondErr := client.Do(req.WithContext(ctx))
		if secondErr == nil {
			secondResp.Body.Close()
			if validateStatus(secondResp.StatusCode, matcher) == nil {
				elapsed = time.Since(second)
			}
		}
	}
	t = uint16(min(elapsed.Milliseconds(), int64(^uint16(0))))
	if t == 0 {
		// Zero means "unavailable" to every selector.
		t = 1
	}
	return
}

// validateStatus applies the configured expected_status; without one every
// response counts as success.
func validateStatus(code int, matcher *StatusMatcher) error {
	if matcher == nil || matcher.Match(code) {
		return nil
	}
	return E.New("urltest: status ", strconv.Itoa(code), " not in expected-status=", matcher.String())
}
