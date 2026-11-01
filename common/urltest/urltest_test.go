package urltest

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"

	"github.com/stretchr/testify/require"
)

type countingDialer struct {
	adapter.Outbound
	dials     atomic.Int32
	multiplex bool
	failing   bool
	release   chan struct{}
}

func (d *countingDialer) MultiplexEnabled() bool {
	return d.multiplex
}

func (d *countingDialer) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	d.dials.Add(1)
	if d.release != nil {
		select {
		case <-d.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if d.failing {
		return nil, E.New("dial failure")
	}
	return (&net.Dialer{}).DialContext(ctx, network, destination.String())
}

func newStatusServer(t *testing.T, status int, requests *atomic.Int32) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests != nil {
			requests.Add(1)
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestURLTestSingleConnectionPerProbe(t *testing.T) {
	var requests atomic.Int32
	server := newStatusServer(t, http.StatusNoContent, &requests)
	dialer := &countingDialer{}

	delay, err := URLTest(context.Background(), server.URL+"/generate_204", dialer)
	require.NoError(t, err)
	require.NotZero(t, delay)
	require.EqualValues(t, 1, dialer.dials.Load())
	require.EqualValues(t, 1, requests.Load())

	delay, err = URLTest(ContextWithUnifiedDelay(context.Background(), true), server.URL+"/generate_204", dialer)
	require.NoError(t, err)
	require.NotZero(t, delay)
	// The unified delay request reuses the probe connection.
	require.EqualValues(t, 2, dialer.dials.Load())
	require.EqualValues(t, 3, requests.Load())
}

func TestURLTestMultiplexWarmUp(t *testing.T) {
	var requests atomic.Int32
	server := newStatusServer(t, http.StatusNoContent, &requests)
	dialer := &countingDialer{multiplex: true}

	_, err := URLTest(context.Background(), server.URL, dialer)
	require.NoError(t, err)
	require.EqualValues(t, 2, dialer.dials.Load())
	require.EqualValues(t, 2, requests.Load())

	// With unified delay the warm-up still opens the session; the measured
	// stream then sends two requests.
	_, err = URLTest(ContextWithUnifiedDelay(context.Background(), true), server.URL, dialer)
	require.NoError(t, err)
	require.EqualValues(t, 4, dialer.dials.Load())
	require.EqualValues(t, 6, requests.Load())
}

func TestURLTestStatusPolicy(t *testing.T) {
	ok := newStatusServer(t, http.StatusOK, nil)
	serverError := newStatusServer(t, http.StatusInternalServerError, nil)
	dialer := &countingDialer{}
	ctx := context.Background()

	// Without expected_status every response counts.
	_, err := URLTestWithStatus(ctx, ok.URL+"/generate_204", dialer, nil)
	require.NoError(t, err)
	_, err = URLTestWithStatus(ctx, serverError.URL+"/", dialer, nil)
	require.NoError(t, err)

	matcher, err := ParseExpectedStatus("500")
	require.NoError(t, err)
	_, err = URLTestWithStatus(ctx, serverError.URL+"/", dialer, matcher)
	require.NoError(t, err)
	matcher, err = ParseExpectedStatus("200-299")
	require.NoError(t, err)
	_, err = URLTestWithStatus(ctx, serverError.URL+"/", dialer, matcher)
	require.ErrorContains(t, err, "expected-status=200-299")
	_, err = URLTestWithStatus(ctx, ok.URL+"/generate_204", dialer, matcher)
	require.NoError(t, err)
}

func TestProberSharesConcurrentProbes(t *testing.T) {
	server := newStatusServer(t, http.StatusNoContent, nil)
	release := make(chan struct{})
	dialer := &countingDialer{release: release}
	var prober Prober
	request := ProbeRequest{Tag: "node", Link: server.URL, Dialer: dialer, Timeout: 5 * time.Second, Since: time.Now()}

	var (
		waitGroup sync.WaitGroup
		results   [4]ProbeResult
	)
	for i := range results {
		waitGroup.Go(func() {
			results[i] = prober.Probe(context.Background(), request)
		})
	}
	require.Eventually(t, func() bool { return dialer.dials.Load() == 1 }, time.Second, time.Millisecond)
	close(release)
	waitGroup.Wait()
	require.EqualValues(t, 1, dialer.dials.Load())
	for _, result := range results {
		require.NoError(t, result.Err)
		require.Equal(t, results[0], result)
	}

	// Finished results are reused back to the caller's horizon only.
	require.Equal(t, results[0], prober.Probe(context.Background(), request))
	require.EqualValues(t, 1, dialer.dials.Load())
	request.Since = time.Now()
	require.NoError(t, prober.Probe(context.Background(), request).Err)
	require.EqualValues(t, 2, dialer.dials.Load())

	// Another URL or status policy is another measurement.
	request.Link = server.URL + "/other"
	require.NoError(t, prober.Probe(context.Background(), request).Err)
	request.Status = MatchAny()
	require.NoError(t, prober.Probe(context.Background(), request).Err)
	require.EqualValues(t, 4, dialer.dials.Load())
}

func TestProberSharesFailures(t *testing.T) {
	dialer := &countingDialer{failing: true}
	var prober Prober
	request := ProbeRequest{Tag: "node", Link: "http://127.0.0.1:1/", Dialer: dialer, Since: time.Now()}
	require.Error(t, prober.Probe(context.Background(), request).Err)
	require.Error(t, prober.Probe(context.Background(), request).Err)
	require.EqualValues(t, 1, dialer.dials.Load())
}

func TestProberDoesNotShareCancellation(t *testing.T) {
	server := newStatusServer(t, http.StatusNoContent, nil)
	release := make(chan struct{})
	dialer := &countingDialer{release: release}
	var prober Prober
	request := ProbeRequest{Tag: "node", Link: server.URL, Dialer: dialer, Since: time.Now()}

	ownerCtx, cancelOwner := context.WithCancel(context.Background())
	ownerDone := make(chan ProbeResult, 1)
	go func() { ownerDone <- prober.Probe(ownerCtx, request) }()
	require.Eventually(t, func() bool { return dialer.dials.Load() == 1 }, time.Second, time.Millisecond)
	joinerDone := make(chan ProbeResult, 1)
	go func() { joinerDone <- prober.Probe(context.Background(), request) }()

	cancelOwner()
	require.ErrorIs(t, (<-ownerDone).Err, context.Canceled)
	// The joiner measures on its own instead of inheriting the owner's
	// cancellation.
	require.Eventually(t, func() bool { return dialer.dials.Load() == 2 }, time.Second, time.Millisecond)
	close(release)
	require.NoError(t, (<-joinerDone).Err)
}

func TestProberTimeoutIsAVerdict(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	dialer := &countingDialer{release: release}
	var prober Prober
	request := ProbeRequest{Tag: "node", Link: "http://127.0.0.1:1/", Dialer: dialer, Timeout: 10 * time.Millisecond, Since: time.Now()}
	require.ErrorIs(t, prober.Probe(context.Background(), request).Err, context.DeadlineExceeded)
	require.ErrorIs(t, prober.Probe(context.Background(), request).Err, context.DeadlineExceeded)
	require.EqualValues(t, 1, dialer.dials.Load())
}
