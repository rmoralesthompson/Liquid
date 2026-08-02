package liquid

import (
	"sync"
	"testing"
)

// BehaviorSubject is white-box tested here for the same reason the CSRF
// codec is: subscriber bookkeeping (does cancel actually remove the
// subscriber?) is not observable at the HTTP seam, while everything
// wire-visible about push lives in sse_test.go and liquidtest/sse_test.go.

func TestBehaviorSubjectHoldsItsCurrentValue(t *testing.T) {
	s := NewBehaviorSubject(41)
	if got := s.Value(); got != 41 {
		t.Fatalf("Value() before any Next = %d, want the initial 41", got)
	}
	s.Next(42)
	if got := s.Value(); got != 42 {
		t.Fatalf("Value() after Next(42) = %d, want 42", got)
	}
}

func TestBehaviorSubjectDeliversEmissionsToSubscribers(t *testing.T) {
	s := NewBehaviorSubject("initial")
	var got []string
	cancel := s.Subscribe(func(v string) { got = append(got, v) })
	defer cancel()

	s.Next("first")
	s.Next("second")
	if len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("subscriber saw %v, want [first second]", got)
	}
}

func TestBehaviorSubjectCancelStopsDelivery(t *testing.T) {
	s := NewBehaviorSubject(0)
	calls := 0
	cancel := s.Subscribe(func(int) { calls++ })
	s.Next(1)
	cancel()
	cancel() // idempotent: a double cancel must not panic or remove a neighbor
	s.Next(2)
	if calls != 1 {
		t.Fatalf("subscriber called %d times, want 1 (nothing after cancel)", calls)
	}
	if n := s.subscriberCount(); n != 0 {
		t.Fatalf("subscriberCount() after cancel = %d, want 0", n)
	}
}

func TestBehaviorSubjectIsSafeUnderConcurrentUse(t *testing.T) {
	s := NewBehaviorSubject(0)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 100 {
				s.Next(i*100 + j)
				_ = s.Value()
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				cancel := s.Subscribe(func(int) {})
				cancel()
			}
		}()
	}
	wg.Wait()
	if n := s.subscriberCount(); n != 0 {
		t.Fatalf("subscriberCount() after all cancels = %d, want 0", n)
	}
}
