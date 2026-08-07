package liquid

// This file is the production HTTP serving helper (#101). App.Serve wraps an
// http.Server with the timeouts a public deployment needs and a graceful
// shutdown that drains live SSE streams — the piece a bare
// http.ListenAndServe(addr, app) cannot do, because http.Server.Shutdown does
// not cancel a request context, so an idle SSE handler (which only returns on
// its stream closing or the request context ending) would hang shutdown.

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// ServeConfig configures App.Serve. The zero value is usable: an unset field
// takes its documented default.
type ServeConfig struct {
	// Addr is the TCP listen address, e.g. ":8080". Required by App.Serve.
	Addr string

	// ReadHeaderTimeout bounds how long reading request headers may take — the
	// guard against a Slowloris client holding a connection open. Default 5s.
	ReadHeaderTimeout time.Duration

	// IdleTimeout bounds how long an idle keep-alive connection is kept open.
	// Default 2m. It does not apply to an active SSE response, which is a
	// long-lived write rather than an idle connection.
	IdleTimeout time.Duration

	// ShutdownTimeout bounds graceful shutdown: once the context is cancelled,
	// Serve stops accepting connections, closes live SSE streams so their
	// handlers return, and waits up to this long for in-flight requests to
	// finish. Default 15s.
	ShutdownTimeout time.Duration

	// TLSCertFile and TLSKeyFile, when both are set, serve HTTPS. Leaving them
	// empty and terminating TLS at an upstream reverse proxy is equally
	// supported.
	TLSCertFile string
	TLSKeyFile  string
}

const (
	defaultReadHeaderTimeout = 5 * time.Second
	defaultIdleTimeout       = 2 * time.Minute
	defaultShutdownTimeout   = 15 * time.Second
)

func (c ServeConfig) withDefaults() ServeConfig {
	if c.ReadHeaderTimeout == 0 {
		c.ReadHeaderTimeout = defaultReadHeaderTimeout
	}
	if c.IdleTimeout == 0 {
		c.IdleTimeout = defaultIdleTimeout
	}
	if c.ShutdownTimeout == 0 {
		c.ShutdownTimeout = defaultShutdownTimeout
	}
	return c
}

// Serve runs app as a production HTTP server on cfg.Addr until ctx is cancelled,
// then shuts down gracefully. Cancel ctx to trigger shutdown; the idiomatic
// wiring is signal.NotifyContext for SIGINT/SIGTERM:
//
//	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
//	defer stop()
//	if err := app.Serve(ctx, liquid.ServeConfig{Addr: ":8080"}); err != nil {
//		logger.Error("serving", "err", err)
//		os.Exit(1)
//	}
//
// Serve applies production timeouts and, on shutdown, drains live SSE streams so
// the server does not hang on long-lived connections. It returns nil on a clean
// graceful shutdown, or the error that stopped it — a failed bind, a serving
// failure, or a shutdown that exceeded ShutdownTimeout.
func (a *App) Serve(ctx context.Context, cfg ServeConfig) error {
	cfg = cfg.withDefaults()
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("liquid: listen %q: %w", cfg.Addr, err)
	}
	return a.serve(ctx, ln, cfg)
}

// serve is the listener-injectable core of Serve, so a test can drive a real
// server on an ephemeral port and read its address.
func (a *App) serve(ctx context.Context, ln net.Listener, cfg ServeConfig) error {
	srv := &http.Server{
		Handler:           a,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		// WriteTimeout is deliberately unset: an SSE response is a long-lived
		// write, and a WriteTimeout would sever every live stream.
		ErrorLog: slog.NewLogLogger(a.logger.Handler(), slog.LevelError),
	}
	// Shutdown stops accepting connections and waits for in-flight handlers to
	// return — but an idle SSE handler blocks on its stream and never observes
	// Shutdown, since Server.Shutdown does not cancel request contexts. Closing
	// every live stream here unblocks those handlers so Shutdown can complete.
	srv.RegisterOnShutdown(a.drainSessions)

	serveErr := make(chan error, 1)
	go func() {
		if cfg.TLSCertFile != "" && cfg.TLSKeyFile != "" {
			serveErr <- srv.ServeTLS(ln, cfg.TLSCertFile, cfg.TLSKeyFile)
		} else {
			serveErr <- srv.Serve(ln)
		}
	}()

	select {
	case err := <-serveErr:
		// Serve/ServeTLS returned before shutdown was requested: a genuine
		// serving failure (http.ErrServerClosed cannot arise on this path).
		return fmt.Errorf("liquid: serving: %w", err)
	case <-ctx.Done():
	}

	// Derive the shutdown deadline from ctx so its values still propagate, but
	// drop its cancellation: ctx is already cancelled — that is what triggered
	// shutdown — so it cannot itself bound the drain.
	shutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		return fmt.Errorf("liquid: graceful shutdown: %w", err)
	}
	// After Shutdown the serve goroutine returns http.ErrServerClosed; receive
	// it so the goroutine cannot outlive this call.
	<-serveErr
	return nil
}

// drainSessions closes every live interactive session and its SSE streams. It is
// registered as an http.Server OnShutdown hook so a graceful shutdown does not
// hang on idle SSE connections.
func (a *App) drainSessions() {
	a.hydro.drainAll()
}
