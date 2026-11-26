package assert

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Queens-Rocket-Engineering-Team/ctl-havoc/internal/timeline"
)

type Verdict int

const (
	Pass Verdict = iota
	Fail
	Inconclusive
)

type Check struct {
	Always    string
	Where     string
	Within    time.Duration
	StableFor time.Duration

	Never      string
	Eventually string
	MaxGap     time.Duration
	SomeGap    time.Duration
}

type Assertion struct {
	Probe   string
	Phase   timeline.Phase
	FaultID string
	Check   Check
}

func eval(a Assertion, t timeline.Timeline) Verdict {
	w := timeline.Window{
		Phase:   a.Phase,
		FaultID: a.FaultID,
	}
	samples, err := t.Window(a.Probe, w)
	if err != nil {
		return Inconclusive
	}

	if len(samples) == 0 {
		return Inconclusive
	}

	switch {
	case a.Check.Always != "":
		for _, sample := range samples {
			valid, err := match(sample, a.Check.Always)
			if err != nil {
				return Inconclusive
			}

			if valid == false {
				return Fail
			}
		}
		return Pass

	case a.Check.StableFor != 0:
		if a.Check.Where == "" || a.Check.Within == 0 {
			return Inconclusive
		}
		deadline, ok := phaseDeadline(t, a.Phase, a.FaultID, a.Check.Within)
		if ok == false {
			return Inconclusive
		}

		var runStart time.Time
		var inStableRun bool

		for _, sample := range samples {
			sampleEnd := sample.At.Add(sample.Latency)
			if !sampleEnd.Before(deadline) {
				break
			}

			valid, err := match(sample, a.Check.Where)
			if err != nil {
				return Inconclusive
			}
			if valid == false {
				inStableRun = false
				continue
			}

			// restart stable for check
			if inStableRun == false {
				runStart = sample.At
				inStableRun = true
			}
			if sampleEnd.Sub(runStart) >= a.Check.StableFor {
				return Pass
			}
		}
		return Fail

	case a.Check.Never != "":
		for _, sample := range samples {
			valid, err := match(sample, a.Check.Never)
			if err != nil {
				return Inconclusive
			}
			if valid {
				return Fail
			}
		}
		return Pass

	// one matching sample from the phase start, before within deadline
	case a.Check.Eventually != "":
		if a.Check.Within == 0 {
			return Inconclusive
		}
		deadline, ok := phaseDeadline(t, a.Phase, a.FaultID, a.Check.Within)
		if ok == false {
			return Inconclusive
		}

		for _, sample := range samples {
			if !sample.At.Before(deadline) {
				break
			}
			valid, err := match(sample, a.Check.Eventually)
			if err != nil {
				return Inconclusive
			}
			if valid {
				return Pass
			}
		}
		return Fail

	case a.Check.MaxGap != 0:
		if len(samples) < 2 {
			return Inconclusive
		}
		for i := 0; i < len(samples)-1; i++ {
			gap := samples[i+1].At.Sub(samples[i].At)
			if gap > a.Check.MaxGap {
				return Fail
			}
		}
		return Pass

	case a.Check.SomeGap != 0:
		if len(samples) < 2 {
			return Fail
		}
		for i := 0; i < len(samples)-1; i++ {
			gap := samples[i+1].At.Sub(samples[i].At)
			if gap > a.Check.SomeGap {
				return Pass
			}
		}
		return Fail

	default:
		return Inconclusive
	}
}

func match(s timeline.Sample, pred string) (bool, error) {
	if !strings.Contains(pred, "==") {
		return false, fmt.Errorf("invalid expression: %q", pred)
	}

	pred = strings.TrimSpace(pred)
	parts := strings.SplitN(pred, "==", 2)
	if len(parts) != 2 {
		return false, fmt.Errorf("invalid expression: %q", pred)
	}

	key := strings.TrimSpace(parts[0])
	value := strings.TrimSpace(parts[1])

	switch key {
	case "err":
		return equalValues(s.Err.String(), value), nil
	case "latency":
		want, err := time.ParseDuration(unquote(value))
		if err != nil {
			return false, fmt.Errorf("invalid latency: %q", value)
		}
		return s.Latency == want, nil
	default:
		actualVal := s.Fields[key]
		if actualVal != nil {
			return equalValues(actualVal, value), nil
		}
	}

	return false, nil
}

// equalValues compares actual to a predicate RHS with number/string coercion.
func equalValues(actual any, wantRaw string) bool {
	want := unquote(wantRaw)

	if an, ok := asFloat(actual); ok {
		if wn, err := strconv.ParseFloat(want, 64); err == nil {
			return an == wn
		}
	}
	if s, ok := actual.(string); ok {
		if an, err := strconv.ParseFloat(s, 64); err == nil {
			if wn, err := strconv.ParseFloat(want, 64); err == nil {
				return an == wn
			}
		}
		return s == want
	}
	return fmt.Sprint(actual) == want
}

// helper functions
func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		if u, err := strconv.Unquote(s); err == nil {
			return u
		}
	}
	return s
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}

func phaseStart(t timeline.Timeline, phase timeline.Phase, faultID string) (time.Time, bool) {
	var name string
	switch phase {
	case timeline.PhaseBefore:
		name = "scenario:start"
	case timeline.PhaseDuring:
		name = "inject:" + faultID + ":end"
	case timeline.PhaseAfter:
		name = "undo:" + faultID + ":end"
	default:
		return time.Time{}, false
	}
	ev, ok := t.Event(name)
	return ev.At, ok
}

func phaseDeadline(t timeline.Timeline, phase timeline.Phase, faultID string, within time.Duration) (time.Time, bool) {
	start, ok := phaseStart(t, phase, faultID)
	if ok == false {
		return time.Time{}, false
	}
	return start.Add(within), true
}
