package timeline

import (
	"errors"
	"log"
	"sync"
	"time"
)

var (
	ErrEmptyFaultID = errors.New("Timeline: empty FaultID")
	ErrMissingMark  = errors.New("Timeline: required mark not found")
	ErrBadPhase     = errors.New("Timeline: unknown phase")
)

type Timeline interface {
	Append(Sample)
	Mark(Event)
	Window(probe string, w Window) ([]Sample, error)
	Event(name string) (Event, bool)
}

type Sample struct {
	Probe string
	At time.Time
	Latency time.Duration
	Err ErrCode
	ErrMsg string
	Fields map[string]any
}

type Event struct {
	Name string
	At time.Time
}

type Phase int

const (
    PhaseBefore Phase = iota
    PhaseDuring 
    PhaseAfter  
)

type Window struct {
    Phase   Phase
    FaultID string
}

type Store struct {
	mu sync.Mutex 
	Samples []Sample
	Events []Event
}

func (s *Store) Append(sample Sample) { 
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Samples = append(s.Samples, sample)
}

func (s *Store) Mark(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, event := range s.Events {
		if e.Name == event.Name {
			log.Println("Duplicate event names")
		} 
	}
	s.Events = append(s.Events, e)
}


// Window maps Phase to a tuple (from, to) via Event
// then keeps the samples inside [from, to).
func (s *Store) Window(probe string, w Window) ([]Sample, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if w.FaultID == "" {
		return nil, ErrEmptyFaultID
	}

	var samples []Sample
	var from, to time.Time

	switch w.Phase {
	case PhaseBefore:
		startEv, ok := s.eventLocked("scenario:start")
		if !ok {
			return nil, ErrMissingMark
		}
		from = startEv.At

		endEv, ok := s.eventLocked("inject:" + w.FaultID + ":begin")
		if !ok {
			return nil, ErrMissingMark
		}
		to = endEv.At
	case PhaseDuring:
		startEv, ok := s.eventLocked("inject:" + w.FaultID + ":end")
		if !ok {
			return nil, ErrMissingMark
		}
		from = startEv.At

		endEv, ok := s.eventLocked("undo:" + w.FaultID + ":begin")
		if !ok {
			return nil, ErrMissingMark
		}
		to = endEv.At
	case PhaseAfter:
		startEv, ok := s.eventLocked("undo:" + w.FaultID + ":end")
		if !ok {
			return nil, ErrMissingMark
		}
		from = startEv.At

		endEv, ok := s.eventLocked("after:end")
		if !ok {
			return nil, ErrMissingMark
		}
		to = endEv.At
	default:
		return nil, ErrBadPhase
	}

	for _, sample := range s.Samples {
		if sample.Probe == probe {
			start := sample.At
			end := sample.At.Add(sample.Latency)

			// Interval [from, to)
			if !start.Before(from) && end.Before(to) {
				samples = append(samples, sample)
			}
		}
	}
	return samples, nil
}

func (s *Store) Event(name string) (Event, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.eventLocked(name)
}

func (s *Store) eventLocked(name string) (Event, bool) {
	for _, ev := range s.Events {
		if ev.Name == name {
			return ev, true
		}
	}
	return Event{}, false
}