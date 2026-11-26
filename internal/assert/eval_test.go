package assert

import (
	"testing"
	"time"

	"github.com/Queens-Rocket-Engineering-Team/ctl-havoc/internal/timeline"
)

const evalFaultID = "pause-server"
const evalProbe = "server_health"

var evalT0 = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func markEvalAfter(s *timeline.Store) {
	s.Mark(timeline.Event{Name: "scenario:start", At: evalT0})
	s.Mark(timeline.Event{Name: "inject:" + evalFaultID + ":begin", At: evalT0.Add(3 * time.Second)})
	s.Mark(timeline.Event{Name: "inject:" + evalFaultID + ":end", At: evalT0.Add(3*time.Second + 10*time.Millisecond)})
	s.Mark(timeline.Event{Name: "undo:" + evalFaultID + ":begin", At: evalT0.Add(10 * time.Second)})
	s.Mark(timeline.Event{Name: "undo:" + evalFaultID + ":end", At: evalT0.Add(10*time.Second + 10*time.Millisecond)})
	s.Mark(timeline.Event{Name: "after:end", At: evalT0.Add(40 * time.Second)})
}

func healthSample(at time.Time, status int) timeline.Sample {
	return timeline.Sample{
		Probe:   evalProbe,
		At:      at,
		Latency: 5 * time.Millisecond,
		Err:     timeline.ErrNone,
		Fields:  map[string]any{"status": status},
	}
}

func timeoutSample(at time.Time) timeline.Sample {
	return timeline.Sample{
		Probe:   evalProbe,
		At:      at,
		Latency: 5 * time.Millisecond,
		Err:     timeline.ErrTimeout,
	}
}

func TestEval_always(t *testing.T) {
	beforeAt := evalT0.Add(1 * time.Second)
	duringAt := evalT0.Add(3*time.Second + 100*time.Millisecond)

	tests := []struct {
		name    string
		phase   timeline.Phase
		pred    string
		samples []timeline.Sample
		want    Verdict
	}{
		{
			name:  "all match pass",
			phase: timeline.PhaseBefore,
			pred:  `status == 200`,
			samples: []timeline.Sample{
				healthSample(beforeAt, 200),
				healthSample(beforeAt.Add(500*time.Millisecond), 200),
			},
			want: Pass,
		},
		{
			name:  "one miss fail",
			phase: timeline.PhaseBefore,
			pred:  `status == 200`,
			samples: []timeline.Sample{
				healthSample(beforeAt, 200),
				healthSample(beforeAt.Add(500*time.Millisecond), 500),
				healthSample(beforeAt.Add(time.Second), 200),
			},
			want: Fail,
		},
		{
			name:    "empty window inconclusive",
			phase:   timeline.PhaseBefore,
			pred:    `status == 200`,
			samples: nil,
			want:    Inconclusive,
		},
		{
			name:  "during timeout pass",
			phase: timeline.PhaseDuring,
			pred:  `err == "timeout"`,
			samples: []timeline.Sample{
				timeoutSample(duringAt),
				timeoutSample(duringAt.Add(time.Second)),
			},
			want: Pass,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &timeline.Store{}
			markEvalAfter(s)
			for _, sample := range tt.samples {
				s.Append(sample)
			}
			a := Assertion{
				Probe:   evalProbe,
				Phase:   tt.phase,
				FaultID: evalFaultID,
				Check:   Check{Always: tt.pred},
			}
			got := eval(a, s)
			if got != tt.want {
				t.Fatalf("eval() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEval_stableFor(t *testing.T) {
	afterStart := evalT0.Add(10*time.Second + 10*time.Millisecond)

	tests := []struct {
		name    string
		samples []timeline.Sample
		want    Verdict
	}{
		{
			name: "five matching seconds pass",
			samples: []timeline.Sample{
				healthSample(afterStart, 200),
				healthSample(afterStart.Add(1*time.Second), 200),
				healthSample(afterStart.Add(2*time.Second), 200),
				healthSample(afterStart.Add(3*time.Second), 200),
				healthSample(afterStart.Add(4*time.Second), 200),
				healthSample(afterStart.Add(5*time.Second), 200),
			},
			want: Pass,
		},
		{
			name: "gap resets run",
			samples: []timeline.Sample{
				healthSample(afterStart, 200),
				healthSample(afterStart.Add(1*time.Second), 200),
				healthSample(afterStart.Add(2*time.Second), 500),
				healthSample(afterStart.Add(3*time.Second), 200),
				healthSample(afterStart.Add(4*time.Second), 200),
			},
			want: Fail,
		},
		{
			name: "stretch ends after within",
			samples: []timeline.Sample{
				healthSample(afterStart.Add(28*time.Second), 200),
				healthSample(afterStart.Add(29*time.Second), 200),
				healthSample(afterStart.Add(30*time.Second), 200),
				healthSample(afterStart.Add(31*time.Second), 200),
				healthSample(afterStart.Add(32*time.Second), 200),
				healthSample(afterStart.Add(33*time.Second), 200),
			},
			want: Fail,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &timeline.Store{}
			markEvalAfter(s)
			for _, sample := range tt.samples {
				s.Append(sample)
			}
			a := Assertion{
				Probe:   evalProbe,
				Phase:   timeline.PhaseAfter,
				FaultID: evalFaultID,
				Check: Check{
					Where:     `status == 200`,
					Within:    30 * time.Second,
					StableFor: 5 * time.Second,
				},
			}
			got := eval(a, s)
			if got != tt.want {
				t.Fatalf("eval() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEval_eventually(t *testing.T) {
	afterStart := evalT0.Add(10*time.Second + 10*time.Millisecond)

	s := &timeline.Store{}
	markEvalAfter(s)
	s.Append(healthSample(afterStart, 500))
	s.Append(healthSample(afterStart.Add(2*time.Second), 200))

	a := Assertion{
		Probe:   evalProbe,
		Phase:   timeline.PhaseAfter,
		FaultID: evalFaultID,
		Check: Check{
			Eventually: `status == 200`,
			Within:     30 * time.Second,
		},
	}
	if got := eval(a, s); got != Pass {
		t.Fatalf("eventually: got %v, want Pass", got)
	}
}

func TestEval_never(t *testing.T) {
	duringStart := evalT0.Add(3*time.Second + 10*time.Millisecond)

	s := &timeline.Store{}
	s.Mark(timeline.Event{Name: "scenario:start", At: evalT0})
	s.Mark(timeline.Event{Name: "inject:" + evalFaultID + ":begin", At: evalT0.Add(3 * time.Second)})
	s.Mark(timeline.Event{Name: "inject:" + evalFaultID + ":end", At: duringStart})
	s.Mark(timeline.Event{Name: "undo:" + evalFaultID + ":begin", At: evalT0.Add(10 * time.Second)})
	s.Mark(timeline.Event{Name: "undo:" + evalFaultID + ":end", At: evalT0.Add(10*time.Second + 10*time.Millisecond)})
	s.Mark(timeline.Event{Name: "after:end", At: evalT0.Add(40 * time.Second)})

	s.Append(timeline.Sample{
		Probe:   evalProbe,
		At:      duringStart.Add(100 * time.Millisecond),
		Latency: 5 * time.Millisecond,
		Err:     timeline.ErrTimeout,
	})

	a := Assertion{
		Probe:   evalProbe,
		Phase:   timeline.PhaseDuring,
		FaultID: evalFaultID,
		Check:   Check{Never: `err == "timeout"`},
	}
	if got := eval(a, s); got != Fail {
		t.Fatalf("never: got %v, want Fail", got)
	}
}

func TestEval_maxGap_someGap(t *testing.T) {
	afterStart := evalT0.Add(10*time.Second + 10*time.Millisecond)

	s := &timeline.Store{}
	markEvalAfter(s)
	s.Append(healthSample(afterStart, 200))
	s.Append(healthSample(afterStart.Add(500*time.Millisecond), 200))
	s.Append(healthSample(afterStart.Add(2*time.Second), 200))

	max := Assertion{
		Probe:   evalProbe,
		Phase:   timeline.PhaseAfter,
		FaultID: evalFaultID,
		Check:   Check{MaxGap: 2 * time.Second},
	}
	if got := eval(max, s); got != Pass {
		t.Fatalf("max_gap: got %v, want Pass", got)
	}

	some := Assertion{
		Probe:   evalProbe,
		Phase:   timeline.PhaseAfter,
		FaultID: evalFaultID,
		Check:   Check{SomeGap: 1 * time.Second},
	}
	if got := eval(some, s); got != Pass {
		t.Fatalf("some_gap: got %v, want Pass", got)
	}
}
