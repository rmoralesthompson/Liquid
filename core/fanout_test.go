package liquid

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"
)

// Fanout is exercised through the public Load/Fanout API on a NewCtx-built
// Ctx — the same reasoning as the CSRF codec and BehaviorSubject tests:
// goroutine bookkeeping (cancellation, wait-for-all) is not observable at
// the HTTP seam. The one wire-visible behavior — a component whose OnInit
// fans out still renders — gets a runtime-seam test at the bottom.

func testCtx(t *testing.T) Ctx {
	t.Helper()
	return NewCtx(httptest.NewRequest("GET", "/", nil), nil)
}

func TestFanoutPopulatesEveryDestination(t *testing.T) {
	var (
		sales   int
		tickets []string
	)
	err := testCtx(t).Fanout(
		Load(&sales, func(ctx context.Context) (int, error) { return 42, nil }),
		Load(&tickets, func(ctx context.Context) ([]string, error) {
			return []string{"a", "b"}, nil
		}),
	)
	if err != nil {
		t.Fatalf("Fanout returned %v, want nil", err)
	}
	if sales != 42 {
		t.Fatalf("sales = %d, want 42", sales)
	}
	if len(tickets) != 2 || tickets[0] != "a" || tickets[1] != "b" {
		t.Fatalf("tickets = %v, want [a b]", tickets)
	}
}

func TestFanoutRunsLoadersConcurrently(t *testing.T) {
	// Each loader signals and then waits for its sibling: only overlapping
	// execution can complete the handshake. The timeout turns a sequential
	// implementation into a clean failure instead of a hung test.
	a, b := make(chan struct{}), make(chan struct{})
	handshake := func(mine, theirs chan struct{}) (bool, error) {
		close(mine)
		select {
		case <-theirs:
			return true, nil
		case <-time.After(5 * time.Second):
			return false, errors.New("sibling loader never started")
		}
	}
	var aMet, bMet bool
	err := testCtx(t).Fanout(
		Load(&aMet, func(ctx context.Context) (bool, error) { return handshake(a, b) }),
		Load(&bMet, func(ctx context.Context) (bool, error) { return handshake(b, a) }),
	)
	if err != nil || !aMet || !bMet {
		t.Fatalf("handshake failed (err=%v, aMet=%v, bMet=%v): loaders did not run concurrently", err, aMet, bMet)
	}
}

func TestFanoutFirstErrorCancelsSiblings(t *testing.T) {
	errBoom := errors.New("boom")
	failed := make(chan struct{})
	var canceled bool
	err := testCtx(t).Fanout(
		Load(new(int), func(ctx context.Context) (int, error) {
			defer close(failed)
			return 0, errBoom
		}),
		Load(&canceled, func(ctx context.Context) (bool, error) {
			<-failed // sibling has already failed; our ctx must now die
			select {
			case <-ctx.Done():
				return true, nil
			case <-time.After(5 * time.Second):
				return false, errors.New("ctx never canceled after sibling error")
			}
		}),
	)
	if !errors.Is(err, errBoom) {
		t.Fatalf("Fanout returned %v, want the loader's error (errors.Is boom)", err)
	}
	if want := "fanout: boom"; err.Error() != want {
		t.Fatalf("err.Error() = %q, want %q (loader errors gain fanout context)", err, want)
	}
	if !canceled {
		t.Fatal("sibling loader's ctx was not canceled after the first error")
	}
}

func TestFanoutWaitsForEveryLoaderBeforeReturning(t *testing.T) {
	// Each loader holds a pointer into the component struct, so Fanout must
	// not return — even on error — while any loader is still running: a late
	// write would race the render's reads. The read of slow below makes the
	// race detector the enforcer if Fanout ever returns early.
	var slow string
	err := testCtx(t).Fanout(
		Load(new(int), func(ctx context.Context) (int, error) {
			return 0, errors.New("fail fast")
		}),
		Load(&slow, func(ctx context.Context) (string, error) {
			<-ctx.Done() // canceled by the sibling's failure...
			time.Sleep(10 * time.Millisecond)
			return "late write", nil // ...but writes its dst anyway
		}),
	)
	if err == nil {
		t.Fatal("Fanout returned nil, want the fail-fast loader's error")
	}
	if slow != "late write" {
		t.Fatalf("slow = %q, want the canceled loader's write to have completed before Fanout returned", slow)
	}
}

func TestFanoutWithTimeoutBoundsOneSource(t *testing.T) {
	var fast, stuck int
	err := testCtx(t).Fanout(
		Load(&fast, func(ctx context.Context) (int, error) { return 1, nil }),
		Load(&stuck, func(ctx context.Context) (int, error) {
			// A cooperative slow source: block until the per-loader deadline.
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(5 * time.Second):
				return 0, errors.New("per-loader deadline never fired")
			}
		}, WithTimeout(20*time.Millisecond)),
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Fanout returned %v, want context.DeadlineExceeded from the timed-out loader", err)
	}
}

func TestFanoutWithZeroLoadersReturnsNil(t *testing.T) {
	if err := testCtx(t).Fanout(); err != nil {
		t.Fatalf("Fanout() with no loaders = %v, want nil", err)
	}
}

func TestFanoutLoaderCtxDerivesFromTheRequestCtx(t *testing.T) {
	// D18: request cancellation must reach every loader. A loader under an
	// already-dead request sees a done ctx immediately.
	reqCtx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest("GET", "/", nil).WithContext(reqCtx)

	err := NewCtx(req, nil).Fanout(
		Load(new(int), func(ctx context.Context) (int, error) {
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(5 * time.Second):
				return 0, errors.New("request cancellation never reached the loader")
			}
		}),
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Fanout returned %v, want context.Canceled flowing from the request ctx", err)
	}
}

func TestFanoutRepanicsLoaderPanicsOnTheCaller(t *testing.T) {
	// A panic on a bare goroutine would crash the process; re-raising it on
	// the caller lets the router's existing recovery serve the error page.
	defer func() {
		if r := recover(); r != "loader exploded" {
			t.Fatalf("recovered %v, want the loader's panic value", r)
		}
	}()
	_ = testCtx(t).Fanout(
		Load(new(int), func(ctx context.Context) (int, error) { panic("loader exploded") }),
	)
	t.Fatal("Fanout returned instead of re-panicking")
}
