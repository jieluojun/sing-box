package group

import (
	"context"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	adapterOutbound "github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/interrupt"
	"github.com/sagernet/sing-box/common/urltest"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"

	"github.com/stretchr/testify/require"
)

type probeTestOutbound struct {
	adapter.Outbound
	tag       string
	failing   atomic.Bool
	dialCount atomic.Int32
}

func (o *probeTestOutbound) Tag() string {
	return o.tag
}

func (o *probeTestOutbound) Type() string {
	return "test"
}

func (o *probeTestOutbound) Network() []string {
	return []string{N.NetworkTCP, N.NetworkUDP}
}

func (o *probeTestOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	o.dialCount.Add(1)
	if o.failing.Load() {
		return nil, E.New("probe test failure")
	}
	return (&net.Dialer{}).DialContext(ctx, network, destination.String())
}

func newURLTestTestGroup(t *testing.T, link string, count int) (*URLTestGroup, *urltest.HistoryStorage, []*probeTestOutbound) {
	t.Helper()
	members := make([]*probeTestOutbound, 0, count)
	outbounds := make([]adapter.Outbound, 0, count)
	manager := &recursiveURLTestOutboundManager{outbounds: make(map[string]adapter.Outbound, count)}
	for i := range count {
		member := &probeTestOutbound{tag: "member-" + strconv.Itoa(i)}
		manager.outbounds[member.tag] = member
		members = append(members, member)
		outbounds = append(outbounds, member)
	}
	history := urltest.NewHistoryStorage()
	group := &URLTestGroup{
		ctx:            context.Background(),
		outbound:       manager,
		logger:         log.NewNOPFactory().Logger(),
		link:           link,
		interval:       time.Minute,
		tolerance:      50,
		idleTimeout:    time.Hour,
		history:        history,
		close:          make(chan struct{}),
		interruptGroup: interrupt.NewGroup(),
	}
	group.storeOutbounds(outbounds)
	group.markActive()
	return group, history, members
}

func storeTestDelay(history *urltest.HistoryStorage, tag string, delay uint16) {
	history.StoreURLTestHistory(tag, &adapter.URLTestHistory{Time: time.Now(), Delay: delay})
}

func TestURLTestColdStartPicksStableRandomMember(t *testing.T) {
	firstPicks := make(map[string]int)
	for range 20 {
		group, _, _ := newURLTestTestGroup(t, "", 5)
		instance := &URLTest{group: group}
		selected := instance.Selected(N.NetworkTCP)
		require.NotNil(t, selected)
		for range 5 {
			require.Same(t, selected, instance.Selected(N.NetworkTCP))
		}
		firstPicks[selected.Tag()]++
	}
	require.Greater(t, len(firstPicks), 1, "cold start always picked the same member")
}

func TestURLTestSelectTolerance(t *testing.T) {
	group, history, members := newURLTestTestGroup(t, "", 3)
	storeTestDelay(history, members[0].Tag(), 200)
	storeTestDelay(history, members[1].Tag(), 180)
	storeTestDelay(history, members[2].Tag(), 300)
	group.route.Store(&urlTestRoute{tcp: members[0]})

	selected, exists := group.Select(N.NetworkTCP)
	require.True(t, exists)
	require.Same(t, members[0], selected, "the current member within tolerance is kept")

	storeTestDelay(history, members[1].Tag(), 100)
	selected, _ = group.Select(N.NetworkTCP)
	require.Same(t, members[1], selected, "a member faster beyond tolerance wins")

	history.DeleteURLTestHistory(members[0].Tag())
	group.performUpdateCheck()
	require.Same(t, members[1], group.currentRoute().tcp)
	require.Same(t, members[1], group.currentRoute().udp)
}

func TestURLTestFallbackOrder(t *testing.T) {
	group, history, members := newURLTestTestGroup(t, "", 3)
	group.fallback = URLTestFallback{enabled: true, maxDelay: 200}
	storeTestDelay(history, members[0].Tag(), 500)
	storeTestDelay(history, members[1].Tag(), 150)
	storeTestDelay(history, members[2].Tag(), 50)

	selected, _ := group.Select(N.NetworkTCP)
	require.Same(t, members[1], selected, "the first member within max_delay wins")

	storeTestDelay(history, members[1].Tag(), 400)
	storeTestDelay(history, members[2].Tag(), 300)
	selected, _ = group.Select(N.NetworkTCP)
	require.Same(t, members[2], selected, "the fastest member is used when all exceed max_delay")
}

func TestURLTestFailedMemberIsDroppedAndReplaced(t *testing.T) {
	shortenRecoveryDelays(t)
	group, history, members := newURLTestTestGroup(t, startRecoveryTestServer(t), 3)
	for i, member := range members {
		storeTestDelay(history, member.Tag(), uint16(100+i))
	}
	group.route.Store(&urlTestRoute{tcp: members[0]})
	members[0].failing.Store(true)

	group.onOutboundFailed(members[0], N.NetworkTCP)

	require.Eventually(t, func() bool {
		return history.LoadURLTestHistory(members[0].Tag()) == nil && group.currentRoute().tcp == members[1]
	}, 5*time.Second, 10*time.Millisecond)
}

func TestURLTestFailedMemberThatPassesIsTrusted(t *testing.T) {
	shortenRecoveryDelays(t)
	group, history, members := newURLTestTestGroup(t, startRecoveryTestServer(t), 2)
	storeTestDelay(history, members[0].Tag(), 100)
	group.route.Store(&urlTestRoute{tcp: members[0]})

	group.onOutboundFailed(members[0], N.NetworkTCP)
	require.Eventually(t, func() bool {
		return members[0].dialCount.Load() == 1 && !group.probePending.Load()
	}, 5*time.Second, 10*time.Millisecond)
	require.Same(t, members[0], group.currentRoute().tcp)
	require.EqualValues(t, 100, history.LoadURLTestHistory(members[0].Tag()).Delay, "the previous delay is kept")

	group.onOutboundFailed(members[0], N.NetworkTCP)
	time.Sleep(50 * time.Millisecond)
	require.EqualValues(t, 1, members[0].dialCount.Load(), "a member that passed is trusted for a while")
}

func TestURLTestManualSelection(t *testing.T) {
	group, history, members := newURLTestTestGroup(t, startRecoveryTestServer(t), 2)
	instance := &URLTest{ctx: context.Background(), group: group, logger: log.NewNOPFactory().Logger()}
	members[1].failing.Store(true)
	require.False(t, instance.SelectOutbound(members[1].Tag()), "an unavailable member is rejected")
	require.False(t, instance.SelectOutbound("missing"))
	require.True(t, instance.SelectOutbound(members[0].Tag()))
	require.Same(t, members[0], group.currentRoute().tcp)
	require.Same(t, members[0], group.currentRoute().udp)
	require.NotNil(t, history.LoadURLTestHistory(members[0].Tag()))
}

func TestURLTestReplaceOutboundsDropsVanishedSelection(t *testing.T) {
	group, history, members := newURLTestTestGroup(t, "", 3)
	storeTestDelay(history, members[2].Tag(), 100)
	group.route.Store(&urlTestRoute{tcp: members[0], udp: members[0]})

	group.replaceOutbounds([]adapter.Outbound{members[1], members[2]})
	require.Same(t, members[2], group.currentRoute().tcp)
	require.Same(t, members[2], group.currentRoute().udp)
}

func TestURLTestOutboundsMeasuresSharedLeafOnce(t *testing.T) {
	server := startRecoveryTestServer(t)
	group, history, members := newURLTestTestGroup(t, server, 2)
	outbounds := []adapter.Outbound{members[0], members[1], members[0]}
	result := URLTestOutbounds(context.Background(), group.outbound, history, group.logger, outbounds, server, time.Minute, true)
	require.Len(t, result, 2)
	require.EqualValues(t, 1, members[0].dialCount.Load())
	require.EqualValues(t, 1, members[1].dialCount.Load())

	// Without force, fresh results are not measured again.
	URLTestOutbounds(context.Background(), group.outbound, history, group.logger, outbounds, server, time.Minute, false)
	require.EqualValues(t, 1, members[0].dialCount.Load())
}

func TestURLTestExpectedStatus(t *testing.T) {
	server := startRecoveryTestServer(t)
	group, history, members := newURLTestTestGroup(t, server, 1)
	matcher, err := urltest.ParseExpectedStatus("200")
	require.NoError(t, err)
	group.expectedStatus = matcher
	group.CheckOutbounds(context.Background(), true)
	require.Nil(t, history.LoadURLTestHistory(members[0].Tag()), "204 does not satisfy expected_status=200")

	matcher, err = urltest.ParseExpectedStatus("204")
	require.NoError(t, err)
	group.expectedStatus = matcher
	group.CheckOutbounds(context.Background(), true)
	require.NotNil(t, history.LoadURLTestHistory(members[0].Tag()))
}

type pinTestCacheFile struct {
	adapter.CacheFile
	selected map[string]string
	stores   int
}

func (c *pinTestCacheFile) LoadSelected(group string) string {
	return c.selected[group]
}

func (c *pinTestCacheFile) StoreSelected(group string, selected string) error {
	c.stores++
	c.selected[group] = selected
	return nil
}

type tcpOnlyTestOutbound struct {
	probeTestOutbound
}

func (o *tcpOnlyTestOutbound) Network() []string {
	return []string{N.NetworkTCP}
}

func newPinTestURLTest(t *testing.T, cacheFile *pinTestCacheFile, members ...*probeTestOutbound) (*URLTest, *urltest.HistoryStorage) {
	t.Helper()
	group, history, _ := newURLTestTestGroup(t, startRecoveryTestServer(t), 0)
	outbounds := make([]adapter.Outbound, 0, len(members))
	for _, member := range members {
		outbounds = append(outbounds, member)
	}
	group.storeOutbounds(outbounds)
	instance := &URLTest{
		Adapter: adapterOutbound.NewAdapter(C.TypeURLTest, "auto", []string{N.NetworkTCP, N.NetworkUDP}, nil),
		ctx:     service.ContextWith[adapter.CacheFile](context.Background(), cacheFile),
		group:   group,
		link:    group.link,
		logger:  log.NewNOPFactory().Logger(),
	}
	group.pinStore = newURLTestPinStore(instance)
	return instance, history
}

func newPinTestMembers(count int) []*probeTestOutbound {
	members := make([]*probeTestOutbound, 0, count)
	for i := range count {
		members = append(members, &probeTestOutbound{tag: "member-" + strconv.Itoa(i)})
	}
	return members
}

func newPinTestCacheFile() *pinTestCacheFile {
	return &pinTestCacheFile{selected: make(map[string]string)}
}

func TestURLTestRouteAutoCannotOverridePin(t *testing.T) {
	a, b := &probeTestOutbound{tag: "a"}, &probeTestOutbound{tag: "b"}
	tcpOnly := &tcpOnlyTestOutbound{probeTestOutbound{tag: "tcp-only"}}
	route := urlTestRoute{tcp: a, udp: a}.withPin(b)
	require.Equal(t, urlTestRoute{tcp: b, udp: b, pin: b}, route)
	require.Equal(t, route, route.withAuto(N.NetworkTCP, a).withAuto(N.NetworkUDP, a))

	route = urlTestRoute{tcp: a, udp: a}.withPin(tcpOnly)
	require.Equal(t, urlTestRoute{tcp: tcpOnly, udp: a, pin: tcpOnly}, route)
	require.Same(t, b, route.withAuto(N.NetworkUDP, b).udp, "networks the pin does not carry stay automatic")
	require.Same(t, tcpOnly, route.withAuto(N.NetworkTCP, b).tcp)
}

func TestURLTestPinSurvivesHealthChecksAndFailures(t *testing.T) {
	shortenRecoveryDelays(t)
	members := newPinTestMembers(3)
	cacheFile := newPinTestCacheFile()
	instance, history := newPinTestURLTest(t, cacheFile, members...)
	group := instance.group
	require.True(t, instance.SelectOutbound(members[2].Tag()))
	require.Equal(t, members[2].Tag(), instance.PinnedTag())
	require.Equal(t, members[2].Tag(), cacheFile.selected["auto"])

	storeTestDelay(history, members[0].Tag(), 10)
	storeTestDelay(history, members[2].Tag(), 900)
	group.performUpdateCheck()
	require.Same(t, members[2], instance.Selected(N.NetworkTCP), "a faster member does not move the pin")
	require.Same(t, members[2], instance.Selected(N.NetworkUDP))

	members[2].failing.Store(true)
	dials := members[2].dialCount.Load()
	group.onOutboundFailed(members[2], N.NetworkTCP)
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, dials, members[2].dialCount.Load(), "a failure of the pinned member is not probed")
	require.Same(t, members[2], instance.Selected(N.NetworkTCP))
	require.Equal(t, members[2].Tag(), instance.PinnedTag())
}

func TestURLTestPinnedChecksOnlyMeasurePin(t *testing.T) {
	members := newPinTestMembers(3)
	instance, history := newPinTestURLTest(t, newPinTestCacheFile(), members...)
	require.True(t, instance.SelectOutbound(members[1].Tag()))
	before := []int32{members[0].dialCount.Load(), members[1].dialCount.Load(), members[2].dialCount.Load()}

	instance.group.CheckOutbounds(context.Background(), true)
	require.Equal(t, before[0], members[0].dialCount.Load())
	require.Equal(t, before[2], members[2].dialCount.Load())
	require.Greater(t, members[1].dialCount.Load(), before[1])

	// A speed test by the user releases the pin and measures every member.
	_, err := instance.URLTest(context.Background())
	require.NoError(t, err)
	require.Empty(t, instance.PinnedTag())
	for _, member := range members {
		require.NotNil(t, history.LoadURLTestHistory(member.Tag()), member.Tag())
	}
}

func TestURLTestPinReleaseAndPersistence(t *testing.T) {
	members := newPinTestMembers(2)
	cacheFile := newPinTestCacheFile()
	instance, history := newPinTestURLTest(t, cacheFile, members...)
	require.True(t, instance.SelectOutbound(members[1].Tag()))
	require.Equal(t, 1, cacheFile.stores)
	require.True(t, instance.SelectOutbound(members[1].Tag()))
	require.Equal(t, 1, cacheFile.stores, "re-pinning the same member writes nothing")

	// A restarted group restores the pin before traffic starts.
	restored, _ := newPinTestURLTest(t, cacheFile, members...)
	restored.group.restorePin()
	require.Equal(t, members[1].Tag(), restored.PinnedTag())
	require.Same(t, members[1], restored.Selected(N.NetworkTCP))
	require.Equal(t, 1, cacheFile.stores, "restoring writes nothing")

	storeTestDelay(history, members[0].Tag(), 10)
	storeTestDelay(history, members[1].Tag(), 500)
	require.True(t, instance.SelectOutbound(""))
	require.Empty(t, instance.PinnedTag())
	require.Empty(t, cacheFile.selected["auto"])
	require.Same(t, members[0], instance.Selected(N.NetworkTCP), "release moves to the best member at once")
	stores := cacheFile.stores
	_, _ = instance.URLTest(context.Background())
	require.Equal(t, stores, cacheFile.stores, "speed tests in auto mode write nothing")

	// A saved pin whose member is gone is cleared.
	cacheFile.selected["auto"] = "missing"
	gone, _ := newPinTestURLTest(t, cacheFile, members...)
	gone.group.restorePin()
	require.Empty(t, gone.PinnedTag())
	require.Empty(t, cacheFile.selected["auto"])
}

func TestURLTestPinFollowsProviderUpdates(t *testing.T) {
	members := newPinTestMembers(2)
	cacheFile := newPinTestCacheFile()
	instance, _ := newPinTestURLTest(t, cacheFile, members...)
	group := instance.group
	require.True(t, instance.SelectOutbound(members[1].Tag()))

	recreated := &probeTestOutbound{tag: members[1].Tag()}
	group.replaceOutbounds([]adapter.Outbound{members[0], recreated})
	require.Same(t, recreated, instance.Selected(N.NetworkTCP), "the pin follows a recreated member")
	require.Same(t, recreated, instance.Selected(N.NetworkUDP))
	require.Equal(t, members[1].Tag(), instance.PinnedTag())

	group.replaceOutbounds([]adapter.Outbound{members[0]})
	require.Empty(t, instance.PinnedTag())
	require.Empty(t, cacheFile.selected["auto"])
	require.Same(t, members[0], instance.Selected(N.NetworkTCP))
	require.Same(t, members[0], instance.Selected(N.NetworkUDP))
}

func TestURLTestTCPOnlyPinLeavesUDPAutomatic(t *testing.T) {
	members := newPinTestMembers(1)
	tcpOnly := &tcpOnlyTestOutbound{probeTestOutbound{tag: "tcp-only"}}
	instance, history := newPinTestURLTest(t, newPinTestCacheFile(), members...)
	instance.group.storeOutbounds([]adapter.Outbound{members[0], tcpOnly})
	storeTestDelay(history, members[0].Tag(), 100)
	require.True(t, instance.SelectOutbound(tcpOnly.Tag()))
	require.Same(t, tcpOnly, instance.Selected(N.NetworkTCP))
	require.Same(t, members[0], instance.Selected(N.NetworkUDP))

	storeTestDelay(history, tcpOnly.Tag(), 1)
	instance.group.performUpdateCheck()
	require.Same(t, members[0], instance.Selected(N.NetworkUDP))
}

func BenchmarkURLTestSelected(b *testing.B) {
	member := &probeTestOutbound{tag: "member"}
	instance := &URLTest{group: &URLTestGroup{}}
	instance.group.route.Store(&urlTestRoute{tcp: member, udp: member, pin: member})
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = instance.Selected(N.NetworkTCP)
		}
	})
}
