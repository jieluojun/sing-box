package group

import (
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

// urlTestRoute is where a URLTest group sends traffic. It is immutable: the
// dial path reads it with a single atomic load, and every change is a
// transition that publishes a new route as a whole, so TCP, UDP and the pin
// never disagree.
//
// A route is in one of two modes. In auto mode tcp and udp follow the health
// checks. In manual mode pin is the member the user chose; it owns every
// network it carries, and auto decisions only reach the networks it does not
// (a TCP-only pin leaves UDP automatic). Auto writers cannot override a pin
// by construction, because they go through withAuto.
type urlTestRoute struct {
	tcp adapter.Outbound
	udp adapter.Outbound
	pin adapter.Outbound
}

var emptyURLTestRoute = &urlTestRoute{}

func (r *urlTestRoute) get(network string) adapter.Outbound {
	if network == N.NetworkUDP {
		return r.udp
	}
	return r.tcp
}

func (r *urlTestRoute) pinTag() string {
	if r.pin == nil {
		return ""
	}
	return r.pin.Tag()
}

// pinned reports whether network is owned by the pin.
func (r *urlTestRoute) pinned(network string) bool {
	return r.pin != nil && common.Contains(r.pin.Network(), network)
}

func (r urlTestRoute) with(network string, outbound adapter.Outbound) urlTestRoute {
	if network == N.NetworkUDP {
		r.udp = outbound
	} else {
		r.tcp = outbound
	}
	return r
}

// withAuto applies an automatic decision; networks owned by the pin keep it.
func (r urlTestRoute) withAuto(network string, outbound adapter.Outbound) urlTestRoute {
	if outbound == nil || r.pinned(network) {
		return r
	}
	return r.with(network, outbound)
}

// withPin enters manual mode on target.
func (r urlTestRoute) withPin(target adapter.Outbound) urlTestRoute {
	r.pin = target
	for _, network := range []string{N.NetworkTCP, N.NetworkUDP} {
		if common.Contains(target.Network(), network) {
			r = r.with(network, target)
		}
	}
	return r
}

var urlTestNetworks = [...]string{N.NetworkTCP, N.NetworkUDP}

func (g *URLTestGroup) currentRoute() *urlTestRoute {
	if route := g.route.Load(); route != nil {
		return route
	}
	return emptyURLTestRoute
}

// transition computes the next route from the current one and publishes it.
// Transitions are serialized; their effects are derived from the difference
// between the two routes, so no caller handles them by itself:
//   - a changed pin is persisted and logged, still in order with other
//     transitions;
//   - connections through a replaced member are interrupted and the history
//     subscribers are notified, outside the lock.
func (g *URLTestGroup) transition(next func(current urlTestRoute) urlTestRoute) {
	g.routeAccess.Lock()
	previous := g.currentRoute()
	route := next(*previous)
	if route == *previous {
		g.routeAccess.Unlock()
		return
	}
	g.route.Store(&route)
	if previousPin, pin := previous.pinTag(), route.pinTag(); previousPin != pin {
		if pin == "" {
			g.logger.Info("manual pin ", previousPin, " released")
		} else {
			g.logger.Info("manual pin set to ", pin)
		}
		g.pinStore.save(pin)
	}
	g.routeAccess.Unlock()
	for _, network := range urlTestNetworks {
		if replaced := previous.get(network); replaced != nil && replaced != route.get(network) {
			g.interruptGroup.Interrupt(g.interruptExternalConnections)
			break
		}
	}
	if g.history != nil {
		g.history.NotifyUpdated()
	}
}

// setAuto applies an automatic choice for network.
func (g *URLTestGroup) setAuto(network string, outbound adapter.Outbound) {
	g.transition(func(route urlTestRoute) urlTestRoute {
		return route.withAuto(network, outbound)
	})
}

// pinOutbound enters manual mode on target.
func (g *URLTestGroup) pinOutbound(target adapter.Outbound) {
	g.transition(func(route urlTestRoute) urlTestRoute {
		return route.withPin(target)
	})
}

// releasePin returns to auto mode. The networks the pin owned get the
// current best member right away. Reports whether a pin was released.
func (g *URLTestGroup) releasePin() bool {
	var released bool
	g.transition(func(route urlTestRoute) urlTestRoute {
		if route.pin == nil {
			return route
		}
		released = true
		owned := route
		route.pin = nil
		for _, network := range urlTestNetworks {
			if owned.pinned(network) {
				if best, _ := g.selectFor(network, route.get(network)); best != nil {
					route = route.with(network, best)
				}
			}
		}
		return route
	})
	return released
}

// probeScope is what a health check measures: in manual mode only the pin
// carries traffic, so the other members are left alone until the pin is
// released, which saves a connection per member and interval.
func (g *URLTestGroup) probeScope() []adapter.Outbound {
	if pin := g.currentRoute().pin; pin != nil {
		return []adapter.Outbound{pin}
	}
	return g.loadOutbounds()
}

// urlTestPinStore persists the pin in the cache file. It remembers what it
// last wrote, so repeated releases on every speed test cost no disk access.
type urlTestPinStore struct {
	cacheFile adapter.CacheFile
	group     string
	logger    log.Logger
	saved     string
}

func newURLTestPinStore(s *URLTest) *urlTestPinStore {
	if s.Tag() == "" {
		return nil
	}
	cacheFile := service.FromContext[adapter.CacheFile](s.ctx)
	if cacheFile == nil {
		return nil
	}
	return &urlTestPinStore{cacheFile: cacheFile, group: s.Tag(), logger: s.logger}
}

func (p *urlTestPinStore) load() string {
	if p == nil {
		return ""
	}
	p.saved = p.cacheFile.LoadSelected(p.group)
	return p.saved
}

// save is called under the route lock, in order with the transitions.
func (p *urlTestPinStore) save(tag string) {
	if p == nil || p.saved == tag {
		return
	}
	if err := p.cacheFile.StoreSelected(p.group, tag); err != nil {
		p.logger.Error("persist manual pin: ", err)
		return
	}
	p.saved = tag
}

// restorePin re-enters manual mode from the cache file before traffic
// starts. A saved pin whose member is gone is cleared.
func (g *URLTestGroup) restorePin() {
	saved := g.pinStore.load()
	if saved == "" {
		return
	}
	if target := outboundByTag(g.loadOutbounds(), saved); target != nil {
		g.pinOutbound(target)
		return
	}
	g.logger.Debug("cached manual pin ", saved, " is no longer a member, cleared")
	g.pinStore.save("")
}
