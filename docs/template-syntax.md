# Liquid Template Syntax (`.lsx`) Reference

> Status: **design spec** — consolidated from the three source docs, which disagreed in places; where they conflicted, this file is authoritative. Directives compile at build time to standard `html/template` constructs (see [architecture.md](architecture.md)).

An `.lsx` file is HTML plus the directives below. Each component pairs one template with one Go struct; template expressions resolve against that struct's exported fields and registered methods. All interpolated values are contextually auto-escaped by `html/template`.

## Interpolation

| Syntax | Meaning | Compiles to |
| --- | --- | --- |
| `{{ FieldName }}` | Exported struct field (dot-paths allowed: `{{ Form.Value }}`) | `{{ .FieldName }}` |
| `{{ $var }}` | Loop variable introduced by `*goFor` | `{{ $var }}` |

```html
<h2>Welcome, {{ Username }}</h2>
```

## Structural directives

Structural directives (prefix `*`) wrap their host element in a control block. One structural directive per element.

### `*goIf`

```html
<div *goIf="IsAdmin" class="badge">Administrator</div>
```
Renders the element only when the (boolean) field is truthy. Compiles to `{{if .IsAdmin}}<div class="badge">…</div>{{end}}`.

### `*goFor`

```html
<li *goFor="let log of RecentLogs">{{ $log }}</li>
```
Repeats the element for each item. Grammar: `let <ident> of <FieldPath>`. Compiles to `{{range $log := .RecentLogs}}<li>…</li>{{end}}`. Inside the loop, reference the item as `{{ $log }}`.

### `*goTranslate`

```html
<h2 *goTranslate="WELCOME_MSG"></h2>
```
Replaces the element's text content with a server-side translation looked up by key against the active locale. *(Deferred past v0.1; the i18n engine is not yet specified.)*

## Event bindings

```html
<button (click)="TriggerReboot">Reset</button>
```

Binds a browser event to an exported method on the component struct. Handlers have exactly one of two signatures (enforced by `liquid vet` at build time — D11):

```go
func (c *C) TriggerReboot()                    // no payload
func (c *C) RenameDashboard(e liquid.Event)    // needs data: typed accessors + e.Bind(&struct)
```

At build time the compiler:
1. verifies the method exists on the paired struct with one of the two signatures above,
2. adds it to the component's **action allowlist** (compiler-generated from bindings — D10; the server only dispatches allowlisted actions),
3. emits `data-liquid-action="TriggerReboot"`; the fixed runtime script wires the listener and posts `{hydroId, action, payload, csrfToken}` to the server.

v0.1 supports `(click)` and `(submit)` (D12) — `(submit)` also exercises the `<form>` CSRF auto-injection below. `(input)` and `(change)` follow the same pattern later.

## Property (input) bindings — parent → child

```html
<app-user-card [userId]="SelectedUser"></app-user-card>
```

When a template contains another component's selector, the renderer instantiates a fresh child instance and copies the parent field named on the right (`SelectedUser`) into the child field matching the left name (`UserID`, case-insensitive match). Types must be assignable; a mismatch is a **build-time error**, not a silent skip.

## Deferred rendering — `*liquidDefer`

```html
<app-slow-stats *liquidDefer [range]="Days"></app-slow-stats>
```

Marks a child occurrence as **deferred**: the page ships immediately with the element's body as a **fallback**, and the child's real content loads in a background goroutine and is **pushed over the session's SSE stream** when ready (#26). The child's own `OnInit` is the deferred work — the slow data load runs off the request path, so one slow source no longer blocks the whole page.

```html
<!-- author -->
<app-slow-stats *liquidDefer [range]="Days">
  <p>Loading your stats…</p>
</app-slow-stats>
```

- **Opt-in per occurrence.** The directive sits on the child usage site, not the component. The same component renders inline or deferred depending on where it is used.
- **Forces a hydro session.** A deferred occurrence pins an SSE stream onto the page even if nothing else on it is interactive — the completion patch needs somewhere to go.
- **`HydroID` required.** The deferred component's struct must have a `HydroID string` field (the completion swaps at that boundary), enforced at build time (**LSX016**). It must sit on a child-selector element (**LSX015**) and takes no value (**LSX005**).
- **`[input]` bindings** flow into the child exactly as for an inline occurrence; they are captured at render time.
- **Error path.** If the deferred load fails, a generic message is patched into the slot (the failure detail is logged server-side, never shipped). Once loaded, the child is an ordinary live component — its event bindings dispatch and its subject subscriptions push updates.

Compiles the occurrence to a fallback slot, `<div data-hydro-id="{{liquidDefer "app-slow-stats" "range" .Days}}">…fallback…</div>`; completion arrives as a `swap` SSE frame that replaces the slot with the child's rendered root, after which subscription updates are ordinary focus-preserving patches.

## Identity & infrastructure attributes

| Syntax | Meaning |
| --- | --- |
| `[hydroId]` | Marks the component's patchable root element. Compiles to `data-hydro-id="{{ .HydroID }}"`; the struct must have a `HydroID string` field (populated by the framework). Required on any component using event bindings or server push. |
| `<form>` | Automatically receives `<input type="hidden" name="csrf_token" value="{{ .CSRFToken }}">`; the struct must include `CSRFToken string` (framework-populated). |

## Attribute directives (extension point)

Custom attribute directives transform their host element at compile time. Example from the source docs:

```html
<div [appHighlight]="ThemeColor">…</div>
```
→ injects `style="background-color: {{ .ThemeColor }}"`. v0.1 ships the mechanism only if trivially cheap; the registry of user-defined attribute directives is a later feature.

## Route parameter binding (struct tags, not template syntax)

```go
type UserDetail struct {
    UserID string `pathParam:"userId"` // bound from route "/users/:userId"
}
```
Bound before `NgOnInit()`. v0.1: string fields; typed conversion (int, etc.) is a candidate follow-up.

## Complete example

```go
type AccountStatus struct {
    HydroID   string
    CSRFToken string
    NodeID    string   `pathParam:"nodeId"`
    IsActive  bool
    Logs      []string
}

func (c *AccountStatus) Selector() string { return "app-account-status" }

func (c *AccountStatus) OnInit(ctx liquid.Ctx) error {
    // fetch state; fan out goroutines with ctx-bound timeouts (D18)
    return nil
}

func (c *AccountStatus) TriggerReboot() { c.IsActive = false }
```

```html
<div [hydroId] class="card">
  <h2>Node: {{ NodeID }}</h2>
  <div *goIf="IsActive" class="status-ok">● Active</div>
  <ul>
    <li *goFor="let log of Logs">{{ $log }}</li>
  </ul>
  <button (click)="TriggerReboot">Emergency Reset</button>
</div>
```

## Accessibility notes (D21)

- The runtime preserves focus by element **`id`** across `[hydroId]` patches and never overwrites the actively-focused input's value — give stable `id`s to any focusable element inside an interactive component.
- Regions that update via **server push (SSE)** should declare `aria-live="polite"` (or `assertive` for urgent alerts) so assistive tech announces the change; the swap itself emits no announcement.

```html
<div [hydroId] aria-live="polite">
  <span>{{ LiveMetric }}</span>
</div>
```

## Known deviations from the source docs

- `(click)="Method()"` (with parens) appears in some source snippets, `(click)="Method"` in others. **Spec: no parens.**
- Source docs emit `onclick="window.hydroEmit(...)"` inline; spec uses `data-liquid-action` + delegated listener (CSP-friendly, no inline JS).
- Source parser was line-based regex; spec requires HTML-tree parsing, so directives work on multi-line elements and nested structures.
