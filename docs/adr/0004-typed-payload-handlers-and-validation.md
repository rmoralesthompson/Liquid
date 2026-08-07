# ADR-0004: Typed payload handlers and form validation

- **Status:** Accepted
- **Date:** 2026-08-07
- **Tracking issue:** [#105](https://github.com/rmoralesthompson/Liquid/issues/105)
- **Amends:** [ADR-0003](0003-closed-domain-guard-coupling.md) (#85) — see "Relationship to #85" below.
- **Relates to:** decisions D10, D11, D12, D30; the v1.0 milestone (#2)

## Context

Through v0.1, an interactive event handler has exactly two shapes (D11):

```go
func (c *C) Save()                 // no payload
func (c *C) Rename(e liquid.Event) // untyped payload: e.String("x"), e.Bind(&s)
```

`(submit)` (D12) posts the form's fields as a `map[string]string`, and the
handler reaches them through `liquid.Event`. There is **no forms or validation
layer**: an author binds fields by hand, validates inside the handler body, and
has no first-class way to surface per-field errors back into the re-rendered
component. The v1.0 milestone commits to filling that gap
([#105](https://github.com/rmoralesthompson/Liquid/issues/105)).

Two forces shape the design:

- **The framework's posture (D30, D8/D4).** D30 deliberately declined a "general
  schema/validation DSL," admitting only closed-domain enums and a pure boolean
  guard predicate, and deferred richer constraints "until a real case appears."
  The project is stdlib-first and adds public surface reluctantly. A
  string-tag validation DSL (`validate:"required,email"`) would reverse that
  stance and likely pull in a dependency to freeze at v1.0.
- **The #85 gap.** [ADR-0003](0003-closed-domain-guard-coupling.md) recorded that
  a closed-domain enum field is enforced at the seam **only when the action
  declares a `<Name>Guard`**, because the guard's parameter is the *only* place a
  payload struct is named to the compiler — a `func(e liquid.Event)` handler's
  payload is the untyped `liquid.Event`. ADR-0003 explicitly deferred the real
  fix to "when typed-payload handlers land."

This ADR lands them.

## Decision

Add a **third handler shape: a typed payload parameter.**

```go
type SignupForm struct {
    Email string
    Age   int
    Plan  Plan // a closed-domain enum (D30), enforced for free
}

// Optional: validation stays in Go — no string DSL.
func (f SignupForm) Validate() liquid.Errors {
    var errs liquid.Errors
    if !strings.Contains(f.Email, "@") {
        errs.Add("Email", "must be a valid email")
    }
    if f.Age < 18 {
        errs.Add("Age", "must be 18 or older")
    }
    return errs
}

func (c *Signup) Submit(f SignupForm) { // f is already bound and validated
    // ... only reached when the payload is valid ...
}
```

At the dispatch seam, for a typed-payload action the framework:

1. **Binds** the posted `map[string]string` into a fresh `SignupForm` (the same
   reflection binder that backs `Event.Bind`, extended as needed).
2. **Enforces the D30 closed-domain / guard contract** on the bound value — now
   discoverable directly from the handler's parameter type, so a guard is no
   longer required for closed-domain enforcement.
3. **Validates**: if the payload type implements `Validate() liquid.Errors` and
   the result is non-empty, the handler is **not called**; instead the component
   is re-rendered with the errors made available to its template.
4. **Dispatches** to the handler only when binding, the contract, and validation
   all pass.

New public surface (committed at v1.0):

- **`liquid.Errors`** — an ordered collection of `(field, message)` validation
  errors with `Add(field, message string)`, emptiness/length inspection, and
  per-field lookup. Returned by a `Validate()` method and read by the template.
- **The `Validate() liquid.Errors` convention** — discovered by the compiler and
  the seam exactly as `Actions()`/`<Name>Guard` are (reflection at registration;
  go/types at compile time), so it stays agent-legible and needs no
  registration call.
- **Template access to errors** — a re-render after a failed validation exposes
  the errors to the component's template so it can render them per field. (The
  concrete mechanism — a template helper vs. a framework-populated field — is an
  implementation detail settled in the PR, not by this ADR; the contract is only
  that the template *can* render them.)

**Validation is Go, not a DSL.** A `Validate()` method (arbitrary Go over the
typed struct) is the constraint language — consistent with how D30 chose a Go
guard predicate over a struct-tag DSL. This composes with, rather than replaces,
D30: closed-domain enums and `<Name>Guard` predicates still apply at the seam;
`Validate()` adds author-defined, message-bearing field validation on top.

**Backward compatibility.** The two existing handler shapes are unchanged; the
typed parameter is a third, opt-in shape. `func()` and `func(e liquid.Event)`
handlers keep working exactly as before.

### Relationship to #85 (amends ADR-0003)

ADR-0003 documented the guard↔closed-domain coupling and named its revisit
trigger: "If typed-payload handlers later make the payload discoverable without a
guard, this expectation should flip." They do — for handlers that adopt a typed
payload. Under this ADR the compiler discovers an action's payload type from its
**handler parameter**, so a closed-domain enum field on a typed-payload handler
is enumerated and enforced at the seam **without** a guard.

The coupling is **loosened, not eliminated**: a `func(e liquid.Event)` handler's
payload is still untyped, so its closed domains remain unenforceable without a
guard. ADR-0003's pin (`TestUnguardedClosedDomainIsNotEnforced`, an Event
handler) therefore **still holds** and stays; a new test asserts the typed-payload
handler *does* enforce without a guard. This ADR **amends** ADR-0003: typed
payloads are now the first-class, guard-free path to closed-domain enforcement,
and the recommended shape for any action that carries constrained values.

## Consequences

- **Positive.** Forms get a real, ergonomic path: bind once into a typed struct,
  validate in Go, render errors — with no string DSL and no new dependency,
  keeping the framework's stdlib-first, agent-legible posture. Closes the #85
  gap as a side effect: least-privilege on the value axis (D30) no longer
  depends on declaring a guard.
- **Positive.** The typed parameter gives `liquid vet` and the manifest (D26)
  full static visibility of a form's shape, extending the agent-first tooling
  story to forms.
- **Negative / accepted.** A third handler shape is more signature-recognition
  surface in the compiler and the runtime dispatch, and more public API to
  commit at v1.0 (`liquid.Errors`, the `Validate()` convention). This is the
  cost of a first-class forms story and is bounded — no open-ended DSL.
- **Follow-up.** Update the D30 note and `limitations.md` (which currently point
  at ADR-0003's coupling as intended behavior) to reflect that typed payloads
  now decouple closed-domain enforcement from guards.
