# Deploying Liquid in production

This guide takes a Liquid app from a built binary to a hardened, single-node
production deployment. It assumes you have an app that builds and runs locally
(see the [getting-started guide](getting-started.md)).

Read the **single-node** section first: it is the one deployment constraint that
shapes everything else, and being honest about it up front (D9) saves you a
painful surprise behind a load balancer.

## Single-node first — the constraint that shapes your topology

Liquid v0.1/v1.0 keeps interactive session state **in memory, in the process
that created it** (D2, [ADR-0002](adr/0002-single-node-sessions-v0.1.md)). A live
session — the component instances behind a browser tab's interactivity — exists
only on the node that rendered the page. There is no external session store yet.

**What this means for deployment:**

- **One instance** needs nothing special: run the binary, done.
- **More than one instance** requires **sticky sessions** (session affinity): the
  load balancer must keep each browser pinned to the node that first served it,
  keyed on the `liquid_session` cookie. Without affinity, a request routed to a
  different node finds no session there and interactivity breaks (events return
  404, SSE streams refuse to attach).

Horizontal scaling *without* sticky sessions needs the Redis-backed session
store on the [roadmap](roadmap.md); until it ships, plan for either a single
node or sticky-session affinity. See [limitations.md](limitations.md) for the
full boundary.

## The production server

Serve through `App.Serve`, not a bare `http.ListenAndServe` — it applies
production timeouts and, critically, shuts down gracefully by **draining live
SSE streams** so a deploy or restart does not sever every open session
mid-connection.

```go
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	liquid "github.com/rmoralesthompson/liquid/core"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	app := liquid.New(liquid.WithLogger(logger))
	// ... app.Provide / app.Register / app.Route ...

	// SIGINT/SIGTERM (the signal an orchestrator sends on deploy) cancels ctx,
	// which triggers graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := app.Serve(ctx, liquid.ServeConfig{Addr: ":8080"}); err != nil {
		logger.Error("serving", "err", err)
		os.Exit(1)
	}
}
```

On `SIGTERM`, `Serve` stops accepting new connections, closes every live SSE
stream so its handler returns, waits up to `ShutdownTimeout` for in-flight
requests to finish, then returns `nil`. Give your orchestrator a termination
grace period at least as long as `ShutdownTimeout` (below).

### Tuning `ServeConfig`

Every field has a production-sane default; override only what you need.

| Field | Default | Notes |
| --- | --- | --- |
| `Addr` | — (required) | TCP listen address, e.g. `":8080"`. |
| `ReadHeaderTimeout` | `5s` | Slowloris guard. |
| `IdleTimeout` | `2m` | Idle keep-alive lifetime. Does **not** apply to an active SSE stream, which is a long-lived write, not an idle connection. |
| `ShutdownTimeout` | `15s` | Grace period for the drain. Match your orchestrator's termination grace. |
| `TLSCertFile` / `TLSKeyFile` | — | Set both to serve HTTPS directly; leave empty to terminate TLS upstream. |

There is deliberately **no `WriteTimeout`**: it would sever long-lived SSE
responses. Bound slow clients with `ReadHeaderTimeout` and `IdleTimeout`
instead.

Session-registry bounds (how many sessions/streams the node holds, idle expiry)
are separate, on `WithLimits` — see the `Limits` type and D20. Tune these to
your memory budget under load.

## TLS

Two supported shapes:

1. **Terminate TLS in the app** — set `TLSCertFile` and `TLSKeyFile`:

   ```go
   app.Serve(ctx, liquid.ServeConfig{
       Addr:        ":8443",
       TLSCertFile: "/etc/tls/tls.crt",
       TLSKeyFile:  "/etc/tls/tls.key",
   })
   ```

2. **Terminate TLS upstream** (reverse proxy / load balancer / service mesh) and
   leave the TLS fields empty. This is common in Kubernetes and behind managed
   load balancers.

Either way, the session cookie is marked `Secure`, so **the browser origin must
be HTTPS** (or `localhost`). Serving the app over plain HTTP on a non-localhost
host means the browser drops the cookie and every session looks logged-out — the
app logs a one-time warning when it detects this.

## Reverse proxy and sticky sessions

A typical deployment puts a reverse proxy (nginx, Caddy, an ALB, an Ingress) in
front of the app. Two things matter for Liquid specifically:

**1. Session affinity, if you run more than one node.** Pin on the
`liquid_session` cookie. Example nginx upstream with cookie affinity:

```nginx
upstream liquid_app {
    # Route each session to the same backend (needs nginx-plus 'sticky cookie',
    # or use ip_hash, or an ALB with cookie stickiness).
    server app-1:8080;
    server app-2:8080;
    sticky cookie liquid_session;
}
```

**2. Do not buffer Server-Sent Events.** SSE is how Liquid pushes live updates;
a proxy that buffers the response will hold updates and break interactivity. The
app sends `Content-Type: text/event-stream` and `Cache-Control: no-store`, but
you must also disable proxy buffering and lengthen the read timeout for the
`/hydro-sse` endpoint:

```nginx
location /hydro-sse {
    proxy_pass http://liquid_app;
    proxy_http_version 1.1;
    proxy_set_header Connection "";
    proxy_buffering off;          # stream, don't buffer
    proxy_read_timeout 1h;        # SSE streams are long-lived
    chunked_transfer_encoding off;
}

location / {
    proxy_pass http://liquid_app;
    proxy_http_version 1.1;
}
```

(For nginx specifically, `proxy_buffering off` is the key line; some setups also
honor an `X-Accel-Buffering: no` response header.)

## Health checks

The app serves two probes under the framework namespace, no wiring required:

| Path | Meaning | Behavior |
| --- | --- | --- |
| `/liquid/health` | **Liveness** — is the process up? | `200` whenever the process is serving, including while draining. |
| `/liquid/ready` | **Readiness** — should it receive new traffic? | `200` normally; **`503` once graceful shutdown begins draining**, so the load balancer stops sending new requests while in-flight ones finish. |

Kubernetes probes:

```yaml
livenessProbe:
  httpGet: { path: /liquid/health, port: 8080 }
readinessProbe:
  httpGet: { path: /liquid/ready, port: 8080 }
# Give the drain time to complete on shutdown:
terminationGracePeriodSeconds: 20   # >= ServeConfig.ShutdownTimeout
```

## Observability

Liquid emits observability events through a dependency-free seam — install a
sink with `WithMetrics` and map it onto Prometheus, OpenTelemetry, statsd, or a
log:

```go
app := liquid.New(liquid.WithMetrics(myMetrics)) // implements liquid.Metrics
```

`Metrics` reports each page render, each `/hydro-event` dispatch (with the
status the seam returned — 200, or a 4xx refusal), and SSE connect/disconnect.
For a live-session gauge, scrape `app.LiveSessions()`. Runtime errors already
flow through the `slog` logger you pass to `WithLogger`.

## A minimal container

```dockerfile
# Build
FROM golang:1.23 AS build
WORKDIR /src
COPY . .
# Compile your .lsx templates, then the binary.
RUN go run ./cmd/liquid build ./ui && \
    CGO_ENABLED=0 go build -o /out/app .

# Run
FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/app /app
EXPOSE 8080
ENTRYPOINT ["/app"]
```

The binary is static (`CGO_ENABLED=0`), so a `distroless/static` (or `scratch`)
base works. Serving TLS in-app? Mount the cert/key and point `ServeConfig` at
them; terminating upstream? Nothing more to add.

## Shutdown checklist

- Wire `signal.NotifyContext` for `SIGINT`/`SIGTERM` and pass its context to
  `Serve` — this is what makes shutdown graceful.
- Set the orchestrator's termination grace `>= ShutdownTimeout`.
- Point the readiness probe at `/liquid/ready` so traffic drains before the
  process exits.
- Behind a proxy, disable buffering on `/hydro-sse` and raise its read timeout.
- Running more than one node? Enable `liquid_session` sticky sessions.
