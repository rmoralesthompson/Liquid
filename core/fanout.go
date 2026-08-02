package liquid

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Loader is one concurrent data source in a Ctx.Fanout call. Build it with
// Load; the zero value is not useful.
type Loader struct {
	run     func(ctx context.Context) error
	timeout time.Duration
}

// LoadOption configures one Loader in a Ctx.Fanout call.
type LoadOption func(*Loader)

// WithTimeout bounds one loader: its function sees a context with this
// deadline (in addition to the request's own cancellation). The deadline is
// cooperative — a function that ignores its context is not killed, it just
// delays Fanout's return.
func WithTimeout(d time.Duration) LoadOption {
	return func(l *Loader) { l.timeout = d }
}

// Load pairs a destination field with the function that produces its value,
// for use with Ctx.Fanout. fn's result is stored in *dst only on success.
func Load[T any](dst *T, fn func(ctx context.Context) (T, error), opts ...LoadOption) Loader {
	l := Loader{run: func(ctx context.Context) error {
		v, err := fn(ctx)
		if err != nil {
			return err
		}
		*dst = v
		return nil
	}}
	for _, opt := range opts {
		opt(&l)
	}
	return l
}

// Fanout runs the loaders concurrently and waits for all of them to finish,
// returning the first error any of them produced. It keeps waiting even
// after that first error (or a WithTimeout deadline): each loader holds a
// pointer into the component, so returning while one still runs would let a
// late write race the render. A failure cancels the sibling loaders'
// contexts, but a loader that ignores its context delays Fanout's return —
// it is never killed. If a loader panics, the panic is re-raised here on
// the calling goroutine (taking precedence over any loader error) so the
// router's usual recovery applies.
func (c Ctx) Fanout(loaders ...Loader) error {
	ctx, cancel := context.WithCancel(c.Context)
	defer cancel()
	var (
		wg         sync.WaitGroup
		mu         sync.Mutex
		firstErr   error
		firstPanic any
	)
	for _, l := range loaders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					mu.Lock()
					if firstPanic == nil {
						firstPanic = r
						cancel()
					}
					mu.Unlock()
				}
			}()
			lctx := ctx
			if l.timeout > 0 {
				var lcancel context.CancelFunc
				lctx, lcancel = context.WithTimeout(ctx, l.timeout)
				defer lcancel()
			}
			if err := l.run(lctx); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if firstPanic != nil {
		panic(firstPanic)
	}
	if firstErr != nil {
		return fmt.Errorf("fanout: %w", firstErr)
	}
	return nil
}
