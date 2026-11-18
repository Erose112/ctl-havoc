package probe

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Queens-Rocket-Engineering-Team/ctl-havoc/internal/timeline"
)

// memTimeline is a minimal Timeline that records samples for probe tests.
type memTimeline struct {
	mu      sync.Mutex
	samples []timeline.Sample
}

func (m *memTimeline) Append(s timeline.Sample) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.samples = append(m.samples, s)
}

func (m *memTimeline) Mark(timeline.Event) {}

func (m *memTimeline) Window(string, timeline.Window) ([]timeline.Sample, error) {
	return nil, nil
}

func (m *memTimeline) Event(string) (timeline.Event, bool) {
	return timeline.Event{}, false
}

func (m *memTimeline) snapshot() []timeline.Sample {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]timeline.Sample, len(m.samples))
	copy(out, m.samples)
	return out
}

func waitSample(t *testing.T, tl *memTimeline, timeout time.Duration) timeline.Sample {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if samples := tl.snapshot(); len(samples) > 0 {
			return samples[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for probe sample")
	return timeline.Sample{}
}

// startProbe runs p.Start in the background and returns a stop func that
// cancels the context and waits for Start to return. Cleanup also calls stop.
func startProbe(t *testing.T, p *HTTPProbe, tl timeline.Timeline) (stop func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- p.Start(ctx, tl)
	}()

	var once sync.Once
	var stopErr error
	stop = func() error {
		once.Do(func() {
			cancel()
			select {
			case stopErr = <-errCh:
			case <-time.After(2 * time.Second):
				stopErr = errors.New("probe did not stop after cancel")
			}
		})
		return stopErr
	}
	t.Cleanup(func() {
		if err := stop(); err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("probe stop: %v", err)
		}
	})
	return stop
}

func TestHTTPProbe_Name(t *testing.T) {
	p := newHTTP("health", "http://example", time.Second, time.Second)
	if got := p.Name(); got != "health" {
		t.Fatalf("Name() = %q, want %q", got, "health")
	}
}

func TestHTTPProbe_Start_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	tl := &memTimeline{}
	p := newHTTP("health", srv.URL, 20*time.Millisecond, time.Second)
	startProbe(t, p, tl)

	sample := waitSample(t, tl, 2*time.Second)
	if sample.Probe != "health" {
		t.Fatalf("Probe = %q, want health", sample.Probe)
	}
	if sample.Err != timeline.ErrNone {
		t.Fatalf("Err = %v, want ErrNone (msg=%q)", sample.Err, sample.ErrMsg)
	}
	if sample.ErrMsg != "" {
		t.Fatalf("ErrMsg = %q, want empty", sample.ErrMsg)
	}
	if sample.Fields == nil {
		t.Fatal("Fields is nil")
	}
	status, ok := sample.Fields["status"].(int)
	if !ok || status != http.StatusOK {
		t.Fatalf("Fields[status] = %#v, want %d", sample.Fields["status"], http.StatusOK)
	}
	if sample.Latency < 0 {
		t.Fatalf("Latency = %v, want >= 0", sample.Latency)
	}
	if sample.At.IsZero() {
		t.Fatal("At is zero")
	}
}

func TestHTTPProbe_Start_BodyTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(make([]byte, maxBody+1))
	}))
	defer srv.Close()

	tl := &memTimeline{}
	p := newHTTP("health", srv.URL, 20*time.Millisecond, 5*time.Second)
	startProbe(t, p, tl)

	sample := waitSample(t, tl, 3*time.Second)
	if sample.Err != timeline.ErrBadResponse {
		t.Fatalf("Err = %v, want ErrBadResponse (msg=%q)", sample.Err, sample.ErrMsg)
	}
	if sample.ErrMsg != "response body too large" {
		t.Fatalf("ErrMsg = %q, want %q", sample.ErrMsg, "response body too large")
	}
	if sample.Fields != nil {
		t.Fatalf("Fields = %#v, want nil on bad response", sample.Fields)
	}
}

func TestHTTPProbe_Start_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tl := &memTimeline{}
	p := newHTTP("health", srv.URL, 20*time.Millisecond, 30*time.Millisecond)
	startProbe(t, p, tl)

	sample := waitSample(t, tl, 2*time.Second)
	if sample.Err != timeline.ErrTimeout {
		t.Fatalf("Err = %v, want ErrTimeout (msg=%q)", sample.Err, sample.ErrMsg)
	}
	if sample.ErrMsg == "" {
		t.Fatal("ErrMsg empty on timeout")
	}
	if sample.Fields != nil {
		t.Fatalf("Fields = %#v, want nil on timeout", sample.Fields)
	}
}

func TestHTTPProbe_Start_UnknownError(t *testing.T) {
	// Bind and close so the port refuses connections.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	tl := &memTimeline{}
	p := newHTTP("health", "http://"+addr, 20*time.Millisecond, time.Second)
	startProbe(t, p, tl)

	sample := waitSample(t, tl, 2*time.Second)
	if sample.Err != timeline.ErrUnknown {
		t.Fatalf("Err = %v, want ErrUnknown (msg=%q)", sample.Err, sample.ErrMsg)
	}
	if sample.ErrMsg == "" {
		t.Fatal("ErrMsg empty on connection error")
	}
}

func TestHTTPProbe_Start_ContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tl := &memTimeline{}
	// Long interval so cancel happens before the first tick in typical runs.
	p := newHTTP("health", srv.URL, time.Hour, time.Second)
	stop := startProbe(t, p, tl)

	err := stop()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Start = %v, want context.Canceled", err)
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want timeline.ErrCode
	}{
		{name: "nil", err: nil, want: timeline.ErrNone},
		{name: "canceled", err: context.Canceled, want: timeline.ErrCanceled},
		{name: "deadline", err: context.DeadlineExceeded, want: timeline.ErrTimeout},
		{name: "wrapped canceled", err: errors.Join(errors.New("wrap"), context.Canceled), want: timeline.ErrCanceled},
		{name: "os timeout", err: &timeoutError{}, want: timeline.ErrTimeout},
		{name: "net timeout", err: &netTimeoutError{timeout: true}, want: timeline.ErrTimeout},
		{name: "net non-timeout", err: &netTimeoutError{timeout: false}, want: timeline.ErrUnknown},
		{name: "plain", err: errors.New("boom"), want: timeline.ErrUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classify(tt.err); got != tt.want {
				t.Fatalf("classify(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// timeoutError satisfies os.IsTimeout via Timeout().
type timeoutError struct{}

func (e *timeoutError) Error() string   { return "timeout" }
func (e *timeoutError) Timeout() bool   { return true }
func (e *timeoutError) Temporary() bool { return true }

var _ net.Error = (*timeoutError)(nil)

type netTimeoutError struct {
	timeout bool
}

func (e *netTimeoutError) Error() string   { return "net timeout" }
func (e *netTimeoutError) Timeout() bool   { return e.timeout }
func (e *netTimeoutError) Temporary() bool { return true }
