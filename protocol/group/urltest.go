package group

import (
	"context"
	"io"
	"math/rand/v2"
	"net"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/interrupt"
	"github.com/sagernet/sing-box/common/urltest"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/x/list"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/pause"
)

// Recovery timings, mihomo style: a member failing a connection is re-probed
// alone after urlTestFailureProbeDelay; one that passes is trusted for
// urlTestFailureTrustDuration, otherwise its history is dropped and, when no
// member is left alive, failures switch blindly for urlTestEmergencyCooldown
// while a full check runs. With no member alive after startup the check is
// retried along urlTestStartupBackoff, never later than the interval.
var urlTestFailureProbeDelay = 3 * time.Second

const (
	urlTestFailureProbeTimeout  = 2500 * time.Millisecond
	urlTestFailureTrustDuration = 60 * time.Second
	urlTestEmergencyCooldown    = 90 * time.Second
)

var urlTestStartupBackoff = []time.Duration{15 * time.Second, 20 * time.Second, 25 * time.Second}

func RegisterURLTest(registry *outbound.Registry) {
	outbound.Register[option.URLTestOutboundOptions](registry, C.TypeURLTest, NewURLTest)
}

var (
	_ adapter.PreMatchOutboundGroup   = (*URLTest)(nil)
	_ adapter.InterfaceUpdateListener = (*URLTest)(nil)
	_ adapter.Referrer                = (*URLTest)(nil)
)

type URLTest struct {
	outbound.Adapter
	ctx                          context.Context
	outbound                     adapter.OutboundManager
	logger                       log.ContextLogger
	tags                         []string
	link                         string
	interval                     time.Duration
	tolerance                    uint16
	idleTimeout                  time.Duration
	fallback                     URLTestFallback
	group                        *URLTestGroup
	checkAccess                  sync.Mutex
	interruptExternalConnections bool
	providerAccess               sync.Mutex
	providerUpdateCheck          providerUpdateCheckScheduler

	provider       adapter.ProviderManager
	providers      map[string]adapter.Provider
	outboundsCache map[string][]adapter.Outbound

	providerTags    []string
	filter          *memberFilter
	useAllProviders bool
	hidden          bool
	icon            string
	expectedStatus  *urltest.StatusMatcher
}

type URLTestFallback struct {
	enabled  bool
	maxDelay int64
}

func NewURLTest(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.URLTestOutboundOptions) (adapter.Outbound, error) {
	filter, err := newMemberFilter(ctx, options.GroupCommonOption)
	if err != nil {
		return nil, err
	}
	expectedStatus, err := urltest.ParseExpectedStatus(options.ExpectedStatus)
	if err != nil {
		return nil, err
	}
	outbound := &URLTest{
		Adapter:                      outbound.NewAdapter(C.TypeURLTest, tag, []string{N.NetworkTCP, N.NetworkUDP}, options.Outbounds),
		ctx:                          ctx,
		outbound:                     service.FromContext[adapter.OutboundManager](ctx),
		logger:                       logger,
		tags:                         options.Outbounds,
		link:                         options.URL,
		interval:                     time.Duration(options.Interval),
		tolerance:                    options.Tolerance,
		idleTimeout:                  time.Duration(options.IdleTimeout),
		interruptExternalConnections: options.InterruptExistConnections,

		provider:       service.FromContext[adapter.ProviderManager](ctx),
		providers:      make(map[string]adapter.Provider),
		outboundsCache: make(map[string][]adapter.Outbound),

		providerTags:    options.Providers,
		filter:          filter,
		useAllProviders: options.UseAllProviders,
		hidden:          options.Hidden,
		icon:            options.Icon,
		expectedStatus:  expectedStatus,
	}
	for i, tag := range outbound.tags {
		if slices.Contains(outbound.tags[:i], tag) {
			return nil, E.New("duplicate tag: ", tag)
		}
	}
	if options.Fallback.Enabled {
		outbound.fallback = URLTestFallback{
			enabled:  true,
			maxDelay: time.Duration(options.Fallback.MaxDelay).Milliseconds(),
		}
	}
	return outbound, nil
}

// Hidden / Icon expose the dashboard hints from option.GroupCommonOption.
func (s *URLTest) Hidden() bool { return s.hidden }
func (s *URLTest) Icon() string { return s.icon }

func (s *URLTest) Start(stage adapter.StartStage, scope *adapter.Scope) error {
	if stage != adapter.StartStateStart {
		if stage == adapter.StartStateStarted {
			s.group.restorePin()
			s.group.PostStart()
		}
		return nil
	}
	s.providerAccess.Lock()
	defer s.providerAccess.Unlock()
	if s.useAllProviders {
		var providerTags []string
		for _, provider := range s.provider.Providers() {
			providerTags = append(providerTags, provider.Tag())
			s.providers[provider.Tag()] = provider
		}
		s.providerTags = providerTags
	} else {
		for i, tag := range s.providerTags {
			provider, loaded := s.provider.Get(tag)
			if !loaded {
				return E.New("outbound provider ", i, " not found: ", tag)
			}
			s.providers[tag] = provider
		}
	}
	if len(s.tags)+len(s.providerTags) == 0 {
		return E.New("missing outbound and provider tags")
	}

	outbounds := make([]adapter.Outbound, 0, len(s.tags))
	for i, tag := range s.tags {
		detour, loaded := s.outbound.Outbound(tag)
		if !loaded {
			return E.New("outbound ", i, " not found: ", tag)
		}
		outbounds = append(outbounds, detour)
	}
	if len(s.tags) == 0 {
		detour, _ := s.outbound.Outbound("Compatible")
		s.tags = append(s.tags, detour.Tag())
		outbounds = append(outbounds, detour)
	}
	group, err := NewURLTestGroup(s.ctx, s.outbound, s.logger, outbounds, s.link, s.interval, s.tolerance, s.idleTimeout, s.fallback, s.interruptExternalConnections, s.expectedStatus)
	if err != nil {
		return err
	}
	group.pinStore = newURLTestPinStore(s)
	s.group = group
	scope.Add(group.Close)
	for _, providerTag := range s.providerTags {
		p := s.providers[providerTag]
		element := p.RegisterCallback(s.onProviderUpdated)
		scope.Add(func() error { p.UnregisterCallback(element); return nil })
	}
	return nil
}

func (s *URLTest) All() []string {
	outbounds := s.group.loadOutbounds()
	tags := make([]string, 0, len(outbounds))
	for _, detour := range outbounds {
		tags = append(tags, detour.Tag())
	}
	return tags
}

// Selected is on the dial path: one atomic load of the route. Only a cold
// group without a decision yet runs a selection.
func (s *URLTest) Selected(network string) adapter.Outbound {
	if network != N.NetworkUDP {
		network = N.NetworkTCP
	}
	if outbound := s.group.currentRoute().get(network); outbound != nil {
		return outbound
	}
	return s.group.coldSelect(network)
}

func (s *URLTest) AttachConnection(closer io.Closer) func() {
	s.group.Touch()
	return s.group.interruptGroup.Add(closer, true)
}

func (s *URLTest) References() []string {
	group := s.group
	if group == nil {
		return nil
	}
	route := group.currentRoute()
	var references []string
	for _, outbound := range []adapter.Outbound{route.pin, route.tcp, route.udp} {
		if outbound != nil && !slices.Contains(references, outbound.Tag()) {
			references = append(references, outbound.Tag())
		}
	}
	return references
}

func (s *URLTest) SelectPreMatchOutbound(metadata *adapter.InboundContext, selectOutbound func(adapter.Outbound) (adapter.Outbound, adapter.PreMatchAction)) (adapter.Outbound, adapter.PreMatchAction) {
	s.group.Touch()
	network := metadata.Network
	if network != N.NetworkUDP {
		network = N.NetworkTCP
	}
	return selectOutbound(s.Selected(network))
}

// PinnedTag reports the manually pinned member, or "" in auto mode.
func (s *URLTest) PinnedTag() string {
	if s.group == nil {
		return ""
	}
	return s.group.currentRoute().pinTag()
}

// SelectOutbound pins tag once it passes an availability test, or returns to
// auto mode when tag is empty. A pin holds through health checks, connection
// failures and restarts until the user releases it, by selecting an empty
// name or running a speed test of the group.
func (s *URLTest) SelectOutbound(tag string) bool {
	if tag == "" {
		s.releasePin()
		return true
	}
	if s.PinnedTag() == tag {
		return true
	}
	target := outboundByTag(s.group.loadOutbounds(), tag)
	if target == nil {
		s.logger.Error("select outbound not found: ", tag)
		return false
	}
	testCtx, cancel := context.WithTimeout(s.ctx, urlTestFailureProbeTimeout)
	defer cancel()
	delay, err := urltest.URLTestWithStatus(testCtx, s.link, target, s.expectedStatus)
	if err != nil {
		s.logger.Warn("manual select ", tag, " failed availability test: ", err)
		return false
	}
	now := time.Now()
	for _, realTag := range []string{tag, RealTag(target, N.NetworkTCP), RealTag(target, N.NetworkUDP)} {
		if realTag != "" {
			s.group.history.StoreURLTestHistory(realTag, &adapter.URLTestHistory{Time: now, Delay: delay})
		}
	}
	s.group.pinOutbound(target)
	s.group.resetTicker()
	return true
}

// releasePin returns to auto mode. The other members were not measured
// while pinned, so they are refreshed in the background.
func (s *URLTest) releasePin() {
	if !s.group.releasePin() || !s.isGroupActive() {
		return
	}
	s.providerUpdateCheck.Schedule(func() {
		_, _ = s.group.urlTestWait(s.ctx, false)
	})
}

// URLTest serves speed tests requested by the user, which release the pin.
func (s *URLTest) URLTest(ctx context.Context) (map[string]uint16, error) {
	s.group.releasePin()
	return s.group.URLTest(ctx)
}

func (s *URLTest) urlTest(ctx context.Context, force bool) (map[string]uint16, error) {
	return s.group.urlTest(ctx, force)
}

// CheckOutbounds serves user speed tests from the platform API, which
// release the manual pin.
func (s *URLTest) CheckOutbounds() {
	s.group.releasePin()
	s.group.CheckOutbounds(s.ctx, true)
}

func (s *URLTest) PerformUpdateCheck() {
	s.group.performUpdateCheck()
}

func (s *URLTest) InterfaceUpdated(ctx context.Context) {
	group := s.group
	if group == nil {
		return
	}
	if group.pause.IsDevicePaused() || group.pause.IsNetworkPaused() {
		return
	}
	go func() {
		s.checkAccess.Lock()
		defer s.checkAccess.Unlock()
		if ctx.Err() != nil {
			return
		}
		group.CheckOutbounds(ctx, true)
	}()
}

func (s *URLTest) isGroupActive() bool {
	if !s.group.started.Load() {
		return false
	}
	return s.group.idleFor() <= s.group.idleTimeout
}

func (s *URLTest) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	s.group.Touch()
	normNet := N.NetworkTCP
	if N.NetworkName(network) == N.NetworkUDP {
		normNet = N.NetworkUDP
	}

	outbound := s.Selected(normNet)
	if outbound == nil {
		return nil, E.New("missing supported outbound")
	}

	conn, err := outbound.DialContext(ctx, network, destination)
	if err == nil {
		adapter.RecordGroupDial(ctx, s.Tag(), outbound)
		return s.group.interruptGroup.NewConn(conn, interrupt.IsExternalConnectionFromContext(ctx), interrupt.IsResourceDownloadFromContext(ctx)), nil
	}

	s.logger.ErrorContext(ctx, err)
	s.group.onOutboundFailed(outbound, normNet)
	return nil, err
}

func (s *URLTest) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	s.group.Touch()
	outbound := s.Selected(N.NetworkUDP)
	if outbound == nil {
		return nil, E.New("missing supported outbound")
	}

	conn, err := outbound.ListenPacket(ctx, destination)
	if err == nil {
		adapter.RecordGroupDial(ctx, s.Tag(), outbound)
		return s.group.interruptGroup.NewPacketConn(conn, interrupt.IsExternalConnectionFromContext(ctx), interrupt.IsResourceDownloadFromContext(ctx)), nil
	}

	s.logger.ErrorContext(ctx, err)
	s.group.onOutboundFailed(outbound, N.NetworkUDP)
	return nil, err
}

func (s *URLTest) onProviderUpdated(tag string) error {
	s.providerAccess.Lock()
	_, outbounds, outboundsCache, err := collectProviderOutbounds(
		tag,
		s.Dependencies(),
		s.outbound,
		s.providers,
		s.providerTags,
		s.outboundsCache,
		s.filter,
	)
	if err != nil {
		s.providerAccess.Unlock()
		return E.Cause(err, s.Tag())
	}
	s.outboundsCache = outboundsCache
	s.group.replaceOutbounds(outbounds)
	s.providerAccess.Unlock()
	if s.isGroupActive() {
		s.group.access.Lock()
		if s.group.ticker != nil {
			s.group.ticker.Reset(s.group.interval)
		}
		s.group.access.Unlock()
		s.providerUpdateCheck.Schedule(func() {
			_, _ = s.group.urlTestWait(s.ctx, false)
		})
	}
	return nil
}

type URLTestGroup struct {
	ctx                          context.Context
	outbound                     adapter.OutboundManager
	pause                        pause.Manager
	pauseCallback                *list.Element[pause.Callback]
	logger                       log.Logger
	outbounds                    []adapter.Outbound
	outboundsAccess              sync.RWMutex
	link                         string
	interval                     time.Duration
	tolerance                    uint16
	idleTimeout                  time.Duration
	history                      *urltest.HistoryStorage
	checking                     sync.Mutex
	route                        atomic.Pointer[urlTestRoute]
	routeAccess                  sync.Mutex
	pinStore                     *urlTestPinStore
	interruptGroup               *interrupt.Group
	interruptExternalConnections bool
	access                       sync.Mutex
	ticker                       *time.Ticker
	close                        chan struct{}
	started                      atomic.Bool
	lastActive                   atomic.Int64 // urlTestClock reading of the last use
	tickerRunning                atomic.Bool
	closed                       bool

	probePending         atomic.Bool
	cooldownEnd          atomic.Int64
	emergencyCooldownEnd atomic.Int64

	fallback       URLTestFallback
	expectedStatus *urltest.StatusMatcher
}

func NewURLTestGroup(ctx context.Context, outboundManager adapter.OutboundManager, logger log.Logger, outbounds []adapter.Outbound, link string, interval time.Duration, tolerance uint16, idleTimeout time.Duration, fallback URLTestFallback, interruptExternalConnections bool, expectedStatus *urltest.StatusMatcher) (*URLTestGroup, error) {
	if interval == 0 {
		interval = C.DefaultURLTestInterval
	}
	if tolerance == 0 {
		tolerance = 50
	}
	if idleTimeout == 0 {
		idleTimeout = C.DefaultURLTestIdleTimeout
	}
	if interval > idleTimeout {
		return nil, E.New("interval must be less or equal than idle_timeout")
	}
	history := service.PtrFromContext[urltest.HistoryStorage](ctx)
	if history == nil {
		return nil, E.New("missing URL test history storage")
	}
	group := &URLTestGroup{
		ctx:                          ctx,
		outbound:                     outboundManager,
		logger:                       logger,
		link:                         link,
		interval:                     interval,
		tolerance:                    tolerance,
		idleTimeout:                  idleTimeout,
		history:                      history,
		fallback:                     fallback,
		close:                        make(chan struct{}),
		pause:                        service.FromContext[pause.Manager](ctx),
		interruptGroup:               interrupt.NewGroup(),
		interruptExternalConnections: interruptExternalConnections,
		expectedStatus:               expectedStatus,
	}
	group.storeOutbounds(outbounds)
	group.transition(func(route urlTestRoute) urlTestRoute {
		for _, network := range urlTestNetworks {
			outbound, _ := group.selectFor(network, nil)
			route = route.withAuto(network, outbound)
		}
		return route
	})

	return group, nil
}

func (g *URLTestGroup) PostStart() {
	g.access.Lock()
	defer g.access.Unlock()
	if g.closed {
		return
	}
	g.started.Store(true)
	g.markActive()

	go g.startupCheckWithBackoff()
}

// urlTestClockBase anchors lastActive to the monotonic clock, so recording
// activity on every dial is one atomic store without allocation.
var urlTestClockBase = time.Now()

func urlTestClock() int64 {
	return int64(time.Since(urlTestClockBase))
}

func (g *URLTestGroup) markActive() {
	g.lastActive.Store(urlTestClock())
}

func (g *URLTestGroup) idleFor() time.Duration {
	return time.Duration(urlTestClock() - g.lastActive.Load())
}

// Touch records activity and starts the health check loop after an idle
// pause. It runs on every dial; while the loop runs it takes no lock.
func (g *URLTestGroup) Touch() {
	if !g.started.Load() {
		return
	}
	if g.tickerRunning.Load() {
		g.markActive()
		return
	}
	g.access.Lock()
	defer g.access.Unlock()
	g.markActive()
	if g.ticker != nil || g.closed {
		return
	}
	ticker := time.NewTicker(g.interval)
	g.ticker = ticker
	g.tickerRunning.Store(true)
	g.pauseCallback = pause.RegisterTicker(g.pause, ticker, g.interval, nil)
	go g.loopCheck(ticker, g.close)
}

func (g *URLTestGroup) resetTicker() {
	g.access.Lock()
	defer g.access.Unlock()
	g.markActive()
	if g.ticker != nil {
		g.ticker.Reset(g.interval)
	}
}

func (g *URLTestGroup) stopTickerLocked() {
	if g.ticker == nil {
		return
	}
	g.ticker.Stop()
	g.ticker = nil
	g.tickerRunning.Store(false)
	g.pause.UnregisterCallback(g.pauseCallback)
	g.pauseCallback = nil
}

func (g *URLTestGroup) Close() error {
	g.access.Lock()
	defer g.access.Unlock()
	if g.closed {
		return nil
	}
	g.closed = true
	g.started.Store(false)
	g.stopTickerLocked()
	close(g.close)
	return nil
}

// coldSelect makes the first decision for network before any health check.
func (g *URLTestGroup) coldSelect(network string) adapter.Outbound {
	var selected adapter.Outbound
	g.transition(func(route urlTestRoute) urlTestRoute {
		if selected = route.get(network); selected != nil {
			return route
		}
		selected, _ = g.selectFor(network, nil)
		return route.withAuto(network, selected)
	})
	return selected
}

// Select returns the member the route would use for network.
func (g *URLTestGroup) Select(network string) (adapter.Outbound, bool) {
	if network != N.NetworkUDP {
		network = N.NetworkTCP
	}
	route := g.currentRoute()
	if route.pinned(network) {
		return route.pin, true
	}
	return g.selectFor(network, route.get(network))
}

// selectFor ranks the members for network, keeping currentSelected while it
// is within tolerance of the best. It reads no route state, so transitions
// can call it.
func (g *URLTestGroup) selectFor(network string, currentSelected adapter.Outbound) (adapter.Outbound, bool) {

	outbounds := g.loadOutbounds()
	if len(outbounds) == 0 {
		return nil, false
	}

	if g.fallback.enabled {
		var (
			fallbackIgnoreOutboundDelay uint16
			fallbackIgnoreOutbound      adapter.Outbound
		)
		for _, detour := range outbounds {
			if !common.Contains(detour.Network(), network) {
				continue
			}
			history := g.history.LoadURLTestHistory(RealTag(detour, network))
			if history == nil || history.Delay == 0 {
				continue
			}
			if g.fallback.maxDelay > 0 && int64(history.Delay) > g.fallback.maxDelay {
				if fallbackIgnoreOutboundDelay == 0 || history.Delay < fallbackIgnoreOutboundDelay {
					fallbackIgnoreOutboundDelay = history.Delay
					fallbackIgnoreOutbound = detour
				}
				continue
			}
			return detour, true
		}
		if fallbackIgnoreOutbound != nil {
			return fallbackIgnoreOutbound, true
		}
		for _, detour := range outbounds {
			if common.Contains(detour.Network(), network) {
				return detour, false
			}
		}
		return nil, false
	}

	var (
		bestOutbound adapter.Outbound
		bestDelay    uint16
	)

	for _, detour := range outbounds {
		if !common.Contains(detour.Network(), network) {
			continue
		}
		history := g.history.LoadURLTestHistory(RealTag(detour, network))
		if history == nil || history.Delay == 0 {
			continue
		}
		if bestDelay == 0 || history.Delay < bestDelay {
			bestDelay = history.Delay
			bestOutbound = detour
		}
	}

	if bestOutbound == nil {
		var candidates []adapter.Outbound
		for _, detour := range outbounds {
			if common.Contains(detour.Network(), network) {
				candidates = append(candidates, detour)
			}
		}
		if len(candidates) > 0 {
			return candidates[rand.IntN(len(candidates))], false
		}
		return nil, false
	}

	if currentSelected != nil && containsOutbound(outbounds, currentSelected) {
		currentHistory := g.history.LoadURLTestHistory(RealTag(currentSelected, network))
		if currentHistory != nil && currentHistory.Delay > 0 {
			if currentHistory.Delay <= bestDelay+g.tolerance {
				return currentSelected, true
			}
		}
	}

	return bestOutbound, true
}

func (g *URLTestGroup) hasAliveOutbound() bool {
	for _, detour := range g.loadOutbounds() {
		if history := g.history.LoadURLTestHistory(detour.Tag()); history != nil && history.Delay > 0 {
			return true
		}
		if history := g.history.LoadURLTestHistory(RealTag(detour, N.NetworkTCP)); history != nil && history.Delay > 0 {
			return true
		}
		if history := g.history.LoadURLTestHistory(RealTag(detour, N.NetworkUDP)); history != nil && history.Delay > 0 {
			return true
		}
	}
	return false
}

func (g *URLTestGroup) onOutboundFailed(failedOutbound adapter.Outbound, network string) {
	if g.currentRoute().pinned(network) {
		// The pinned member stays until the user releases it, so a failure
		// is neither probed nor acted upon.
		return
	}
	outbounds := g.loadOutbounds()

	if len(outbounds) <= 1 {
		return
	}

	now := time.Now().UnixNano()

	if now < g.cooldownEnd.Load() {
		return
	}

	if now < g.emergencyCooldownEnd.Load() {
		if next, exists := g.Select(network); next != nil && exists {
			g.setAuto(network, next)
		}
		return
	}

	if !g.probePending.CompareAndSwap(false, true) {
		return
	}

	time.AfterFunc(urlTestFailureProbeDelay, func() {
		defer g.probePending.Store(false)

		if g.ctx.Err() != nil {
			return
		}

		testCtx, cancel := context.WithTimeout(g.ctx, urlTestFailureProbeTimeout)
		defer cancel()

		delay, err := urltest.URLTestWithStatus(testCtx, g.link, failedOutbound, g.expectedStatus)
		if err != nil {
			g.history.DeleteURLTestHistory(failedOutbound.Tag())
			g.history.DeleteURLTestHistory(RealTag(failedOutbound, network))

			if next, exists := g.Select(network); next != nil && exists {
				g.setAuto(network, next)
			} else {

				candidates := make([]adapter.Outbound, 0, len(outbounds)-1)
				for _, detour := range outbounds {
					if detour.Tag() != failedOutbound.Tag() && common.Contains(detour.Network(), network) {
						candidates = append(candidates, detour)
					}
				}

				if len(candidates) > 0 {
					target := candidates[rand.IntN(len(candidates))]
					g.setAuto(network, target)
				}

				g.emergencyCooldownEnd.Store(time.Now().Add(urlTestEmergencyCooldown).UnixNano())

				if g.idleFor() <= g.idleTimeout {
					go func() {
						g.CheckOutbounds(g.ctx, false)
						g.resetTicker()
					}()
				}
			}
		} else {
			prevHistory := g.history.LoadURLTestHistory(failedOutbound.Tag())
			effectiveDelay := delay
			if prevHistory != nil && prevHistory.Delay > 0 {
				effectiveDelay = prevHistory.Delay
			}
			g.history.StoreURLTestHistory(failedOutbound.Tag(), &adapter.URLTestHistory{
				Time:  time.Now(),
				Delay: effectiveDelay,
			})

			realTag := RealTag(failedOutbound, network)
			if realTag != failedOutbound.Tag() {
				prevRealHistory := g.history.LoadURLTestHistory(realTag)
				effectiveRealDelay := delay
				if prevRealHistory != nil && prevRealHistory.Delay > 0 {
					effectiveRealDelay = prevRealHistory.Delay
				}
				g.history.StoreURLTestHistory(realTag, &adapter.URLTestHistory{
					Time:  time.Now(),
					Delay: effectiveRealDelay,
				})
			}

			g.cooldownEnd.Store(time.Now().Add(urlTestFailureTrustDuration).UnixNano())
		}
	})
}

func (g *URLTestGroup) startupCheckWithBackoff() {
	if g.hasAliveOutbound() {
		return
	}

	for _, delay := range urlTestStartupBackoff {
		if delay >= g.interval {
			delay = g.interval
		}

		timer := time.NewTimer(delay)
		select {
		case <-g.close:
			timer.Stop()
			return
		case <-g.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		if g.idleFor() > g.idleTimeout {
			return
		}

		if g.hasAliveOutbound() {
			return
		}

		res, _ := g.urlTestWait(g.ctx, true)
		if len(res) > 0 {
			g.logger.Debug("urltest startup probe succeeded")
			return
		} else {
			g.logger.Debug("urltest startup probe failed, retrying after backoff: ", delay)
		}

		if delay == g.interval {
			return
		}
	}
}

func (g *URLTestGroup) loopCheck(ticker *time.Ticker, closeChan <-chan struct{}) {
	if g.idleFor() > g.interval {
		g.markActive()
		g.CheckOutbounds(g.ctx, false)
	}
	for {
		select {
		case <-closeChan:
			return
		case <-ticker.C:
		}
		if g.idleFor() > g.idleTimeout {
			g.access.Lock()
			if g.ticker == ticker {
				g.stopTickerLocked()
			}
			g.access.Unlock()
			return
		}
		g.CheckOutbounds(g.ctx, false)
	}
}

func (g *URLTestGroup) CheckOutbounds(ctx context.Context, force bool) {
	_, _ = g.urlTest(ctx, force)
}

func (g *URLTestGroup) URLTest(ctx context.Context) (map[string]uint16, error) {
	return g.urlTest(ctx, true)
}

func (g *URLTestGroup) urlTest(ctx context.Context, force bool) (map[string]uint16, error) {
	if !g.checking.TryLock() {
		return make(map[string]uint16), nil
	}
	defer g.checking.Unlock()
	return g.urlTestLocked(ctx, force)
}

func (g *URLTestGroup) urlTestWait(ctx context.Context, force bool) (map[string]uint16, error) {
	g.checking.Lock()
	defer g.checking.Unlock()
	return g.urlTestLocked(ctx, force)
}

func (g *URLTestGroup) urlTestLocked(ctx context.Context, force bool) (map[string]uint16, error) {
	ctx = urltest.ContextWithUnifiedDelay(ctx, urltest.UnifiedDelayFromContext(g.ctx))
	result := urlTestOutbounds(ctx, g.outbound, g.history, g.logger, g.probeScope(), g.link, g.expectedStatus, g.interval, force)
	select {
	case <-ctx.Done():
	default:
		g.performUpdateCheck()
	}
	return result, nil
}

// performUpdateCheck moves the networks in auto mode to the best member
// after a health check.
func (g *URLTestGroup) performUpdateCheck() {
	g.transition(func(route urlTestRoute) urlTestRoute {
		for _, network := range urlTestNetworks {
			if route.pinned(network) {
				continue
			}
			current := route.get(network)
			outbound, exists := g.selectFor(network, current)
			if outbound == nil || outbound == current {
				continue
			}
			var currentAlive bool
			if current != nil {
				history := g.history.LoadURLTestHistory(RealTag(current, network))
				currentAlive = history != nil && history.Delay > 0
			}
			if current == nil || exists || !currentAlive {
				route = route.with(network, outbound)
			}
		}
		return route
	})
}

func (g *URLTestGroup) loadOutbounds() []adapter.Outbound {
	g.outboundsAccess.RLock()
	defer g.outboundsAccess.RUnlock()
	return g.outbounds
}

func (g *URLTestGroup) storeOutbounds(outbounds []adapter.Outbound) {
	g.outboundsAccess.Lock()
	g.outbounds = outbounds
	g.outboundsAccess.Unlock()
}

// replaceOutbounds applies a provider update. Providers may recreate members
// under unchanged tags, so the route follows them by tag; a pinned member
// that is gone returns its networks to auto mode.
func (g *URLTestGroup) replaceOutbounds(outbounds []adapter.Outbound) {
	g.transition(func(route urlTestRoute) urlTestRoute {
		g.storeOutbounds(outbounds)
		if route.pin != nil && !slices.Contains(outbounds, route.pin) {
			if replacement := outboundByTag(outbounds, route.pin.Tag()); replacement != nil {
				route = route.withPin(replacement)
			} else {
				route.pin = nil
			}
		}
		for _, network := range urlTestNetworks {
			if route.pinned(network) {
				continue
			}
			if current := route.get(network); current == nil || !slices.Contains(outbounds, current) {
				outbound, _ := g.selectFor(network, nil)
				route = route.with(network, outbound)
			}
		}
		return route
	})
	if g.history != nil {
		g.history.NotifyUpdated()
	}
}

func containsOutbound(outbounds []adapter.Outbound, selected adapter.Outbound) bool {
	if selected == nil {
		return true
	}
	return slices.Contains(outbounds, selected)
}

func outboundByTag(outbounds []adapter.Outbound, tag string) adapter.Outbound {
	for _, detour := range outbounds {
		if detour.Tag() == tag {
			return detour
		}
	}
	return nil
}
