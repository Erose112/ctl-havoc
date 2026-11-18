package timeline

import (
	"errors"
	"sync"
	"testing"
	"time"
)

const testFaultID = "pause-server"
const testProbe = "server_health"

// fixed base time so Window filters stay deterministic
var t0 = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func markServerPause(s *Store) {
	// Matches the mark names Window looks up in timeline.go.
	s.Mark(Event{Name: "scenario:start", At: t0})
	s.Mark(Event{Name: "inject:" + testFaultID + ":begin", At: t0.Add(3 * time.Second)})
	s.Mark(Event{Name: "inject:" + testFaultID + ":end", At: t0.Add(3*time.Second + 10*time.Millisecond)})
	s.Mark(Event{Name: "undo:" + testFaultID + ":begin", At: t0.Add(10 * time.Second)})
	s.Mark(Event{Name: "undo:" + testFaultID + ":end", At: t0.Add(10*time.Second + 10*time.Millisecond)})
	s.Mark(Event{Name: "after:end", At: t0.Add(40 * time.Second)})
}

func sampleAt(at time.Time, latency time.Duration, err ErrCode) Sample {
	return Sample{
		Probe:   testProbe,
		At:      at,
		Latency: latency,
		Err:     err,
	}
}

func mustWindow(t *testing.T, s *Store, probe string, w Window) []Sample {
	t.Helper()
	got, err := s.Window(probe, w)
	if err != nil {
		t.Fatalf("Window: unexpected error: %v", err)
	}
	return got
}

func TestEvent_FindAndMiss(t *testing.T) {
	s := &Store{}
	s.Mark(Event{Name: "scenario:start", At: t0})

	ev, ok := s.Event("scenario:start")
	if !ok {
		t.Fatal("expected to find scenario:start")
	}
	if !ev.At.Equal(t0) {
		t.Fatalf("At = %v, want %v", ev.At, t0)
	}

	if _, ok := s.Event("missing"); ok {
		t.Fatal("expected miss for unknown name")
	}
}

func TestWindow_MissingMarkReturnsError(t *testing.T) {
	s := &Store{}
	got, err := s.Window(testProbe, Window{Phase: PhaseDuring, FaultID: testFaultID})
	if !errors.Is(err, ErrMissingMark) {
		t.Fatalf("want ErrMissingMark, got err=%v samples=%#v", err, got)
	}
	if got != nil {
		t.Fatalf("want nil samples on error, got %#v", got)
	}
}

func TestWindow_EmptyFaultID(t *testing.T) {
	s := &Store{}
	got, err := s.Window(testProbe, Window{Phase: PhaseDuring, FaultID: ""})
	if !errors.Is(err, ErrEmptyFaultID) {
		t.Fatalf("want ErrEmptyFaultID, got err=%v samples=%#v", err, got)
	}
	if got != nil {
		t.Fatalf("want nil samples on error, got %#v", got)
	}
}

func TestWindow_Before(t *testing.T) {
	s := &Store{}
	markServerPause(s)

	// Fully inside [scenario:start, inject:begin) = [t0, t0+3s)
	s.Append(sampleAt(t0.Add(1*time.Second), 100*time.Millisecond, ErrNone))
	// Ends exactly at inject begin → excluded from [from, to)
	s.Append(sampleAt(t0.Add(3*time.Second), 0, ErrNone))
	// Wrong probe
	s.Append(Sample{
		Probe:   "other",
		At:      t0.Add(1 * time.Second),
		Latency: 0,
		Err:     ErrNone,
	})

	got := mustWindow(t, s, testProbe, Window{Phase: PhaseBefore, FaultID: testFaultID})
	if len(got) != 1 {
		t.Fatalf("before: want 1 sample, got %d (%#v)", len(got), got)
	}
	if got[0].Err != ErrNone {
		t.Fatalf("before: Err = %v, want ErrNone", got[0].Err)
	}
}

func TestWindow_During(t *testing.T) {
	s := &Store{}
	markServerPause(s)

	// during = [inject:end, undo:begin) ≈ [t0+3.010s, t0+10s)
	s.Append(sampleAt(t0.Add(5*time.Second), 100*time.Millisecond, ErrTimeout))
	s.Append(sampleAt(t0.Add(8*time.Second), 50*time.Millisecond, ErrTimeout))

	// Straddles inject: starts before inject:end, ends after → excluded
	s.Append(sampleAt(
		t0.Add(3*time.Second+10*time.Millisecond-30*time.Millisecond), // inject:end - 30ms
		50*time.Millisecond, // ends at inject:end + 20ms
		ErrTimeout,
	))

	// Still in before phase
	s.Append(sampleAt(t0.Add(2*time.Second), 0, ErrNone))

	got := mustWindow(t, s, testProbe, Window{Phase: PhaseDuring, FaultID: testFaultID})
	if len(got) != 2 {
		t.Fatalf("during: want 2 samples, got %d (%#v)", len(got), got)
	}
	for i, sm := range got {
		if sm.Err != ErrTimeout {
			t.Fatalf("during[%d]: Err = %v, want ErrTimeout", i, sm.Err)
		}
	}
}

func TestWindow_After(t *testing.T) {
	s := &Store{}
	markServerPause(s)

	// after = [undo:end, after:end) ≈ [t0+10.010s, t0+40s)
	s.Append(sampleAt(t0.Add(15*time.Second), 100*time.Millisecond, ErrNone))
	// Ends at after:end → excluded from [from, to)
	s.Append(sampleAt(t0.Add(40*time.Second), 0, ErrNone))

	got := mustWindow(t, s, testProbe, Window{Phase: PhaseAfter, FaultID: testFaultID})
	if len(got) != 1 {
		t.Fatalf("after: want 1 sample, got %d (%#v)", len(got), got)
	}
}

func TestAppend_ConcurrentRace(t *testing.T) {
	s := &Store{}
	markServerPause(s)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.Append(sampleAt(t0.Add(5*time.Second+time.Duration(i)*time.Millisecond), 0, ErrTimeout))
		}(i)
	}
	wg.Wait()

	got := mustWindow(t, s, testProbe, Window{Phase: PhaseDuring, FaultID: testFaultID})
	if len(got) != 50 {
		t.Fatalf("concurrent append: want 50 during samples, got %d", len(got))
	}
}

func TestEvent_DuplicateNameReturnsFirst(t *testing.T) {
	s := &Store{}
	s.Mark(Event{Name: "scenario:start", At: t0})
	s.Mark(Event{Name: "scenario:start", At: t0.Add(time.Hour)}) // Mark logs but still appends

	ev, ok := s.Event("scenario:start")
	if !ok {
		t.Fatal("expected find")
	}
	if !ev.At.Equal(t0) {
		t.Fatalf("Event returns first match: At = %v, want %v", ev.At, t0)
	}
}
