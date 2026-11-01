package group

import (
	"context"
	"maps"
	"sync"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/batch"
	N "github.com/sagernet/sing/common/network"
)

type urlTestResult struct {
	delay uint16
	err   error
}

type urlTestSessionKey struct {
	tag    string
	link   string
	status string
}

type urlTestSessionCall struct {
	done   chan struct{}
	result urlTestResult
}

type urlTestSession struct {
	access sync.Mutex
	calls  map[urlTestSessionKey]*urlTestSessionCall
}

type urlTestSessionContextKey struct{}

type recursiveURLTestGroup interface {
	adapter.OutboundGroup
	urlTest(ctx context.Context, force bool) (map[string]uint16, error)
}

func urlTestSessionFromContext(ctx context.Context) (context.Context, *urlTestSession) {
	if session, loaded := ctx.Value(urlTestSessionContextKey{}).(*urlTestSession); loaded {
		return ctx, session
	}
	session := &urlTestSession{calls: make(map[urlTestSessionKey]*urlTestSessionCall)}
	return context.WithValue(ctx, urlTestSessionContextKey{}, session), session
}

func (s *urlTestSession) test(ctx context.Context, key urlTestSessionKey, test func() urlTestResult) (result urlTestResult) {
	s.access.Lock()
	call, loaded := s.calls[key]
	if !loaded {
		call = &urlTestSessionCall{done: make(chan struct{})}
		s.calls[key] = call
	}
	s.access.Unlock()
	if loaded {
		select {
		case <-call.done:
			return call.result
		case <-ctx.Done():
			return urlTestResult{err: ctx.Err()}
		}
	}
	defer func() {
		call.result = result
		close(call.done)
	}()
	return test()
}

type urlTestBatch struct {
	ctx      context.Context
	outbound adapter.OutboundManager
	history  adapter.URLTestHistoryStorage
	logger   log.Logger
	session  *urlTestSession
	batch    *batch.Batch[any]
	checked  map[string]bool
	groups   []adapter.OutboundGroup
	access   sync.Mutex
	result   map[string]uint16
}

// URLTestOutbounds tests outbounds and the members of nested groups. Without
// force, leaves measured within interval are skipped. Leaves reachable through
// several nested groups are measured once per call.
func URLTestOutbounds(ctx context.Context, outboundManager adapter.OutboundManager, history adapter.URLTestHistoryStorage, logger log.Logger, outbounds []adapter.Outbound, link string, interval time.Duration, force bool) map[string]uint16 {
	return urlTestOutbounds(ctx, outboundManager, history, logger, outbounds, link, nil, interval, force)
}

func urlTestOutbounds(ctx context.Context, outboundManager adapter.OutboundManager, history adapter.URLTestHistoryStorage, logger log.Logger, outbounds []adapter.Outbound, link string, matcher *urltest.StatusMatcher, interval time.Duration, force bool) map[string]uint16 {
	ctx, session := urlTestSessionFromContext(ctx)
	b, _ := batch.New(ctx, batch.WithConcurrencyNum[any](10))
	testBatch := &urlTestBatch{
		ctx:      ctx,
		outbound: outboundManager,
		history:  history,
		logger:   logger,
		session:  session,
		batch:    b,
		checked:  make(map[string]bool),
		result:   make(map[string]uint16),
	}
	testBatch.test(outbounds, link, matcher, interval, force)
	b.Wait()
	for _, outboundGroup := range testBatch.groups {
		groupHistory := history.LoadURLTestHistory(RealTag(outboundGroup, N.NetworkTCP))
		if groupHistory != nil {
			testBatch.result[outboundGroup.Tag()] = groupHistory.Delay
		}
	}
	return testBatch.result
}

func (b *urlTestBatch) test(outbounds []adapter.Outbound, link string, matcher *urltest.StatusMatcher, interval time.Duration, force bool) {
	var status string
	if matcher != nil {
		status = matcher.String()
	}
	for _, detour := range outbounds {
		tag := detour.Tag()
		if b.checked[tag] {
			continue
		}
		switch nested := detour.(type) {
		case recursiveURLTestGroup:
			b.checked[tag] = true
			b.groups = append(b.groups, nested)
			b.batch.Go(tag, func() (any, error) {
				nestedResult, _ := nested.urlTest(b.ctx, force)
				b.access.Lock()
				maps.Copy(b.result, nestedResult)
				b.access.Unlock()
				return nil, nil
			})
		case adapter.OutboundGroup:
			b.checked[tag] = true
			b.groups = append(b.groups, nested)
			b.test(common.FilterNotNil(common.Map(nested.All(), func(it string) adapter.Outbound {
				member, _ := b.outbound.Outbound(it)
				return member
			})), link, matcher, interval, force)
		default:
			history := b.history.LoadURLTestHistory(tag)
			if !force && history != nil && time.Since(history.Time) < interval {
				continue
			}
			b.checked[tag] = true
			b.batch.Go(tag, func() (any, error) {
				testResult := b.session.test(b.ctx, urlTestSessionKey{tag: tag, link: link, status: status}, func() urlTestResult {
					testCtx, cancel := context.WithTimeout(b.ctx, C.TCPTimeout)
					defer cancel()
					testChan := make(chan urlTestResult, 1)
					go func() {
						delay, testErr := urltest.URLTestWithStatus(testCtx, link, detour, matcher)
						testChan <- urlTestResult{delay, testErr}
					}()
					select {
					case testResult := <-testChan:
						return testResult
					case <-testCtx.Done():
						return urlTestResult{err: testCtx.Err()}
					}
				})
				if testResult.err != nil {
					if b.ctx.Err() != nil {
						return nil, nil
					}
					b.logger.Debug("outbound ", tag, " unavailable: ", testResult.err)
					b.history.DeleteURLTestHistory(tag)
				} else {
					b.logger.Debug("outbound ", tag, " available: ", testResult.delay, "ms")
					b.history.StoreURLTestHistory(tag, &adapter.URLTestHistory{
						Time:  time.Now(),
						Delay: testResult.delay,
					})
					b.access.Lock()
					b.result[tag] = testResult.delay
					b.access.Unlock()
				}
				return nil, nil
			})
		}
	}
}
