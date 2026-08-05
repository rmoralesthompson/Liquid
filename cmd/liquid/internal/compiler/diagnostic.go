package compiler

// Severity classifies how serious a Diagnostic is.
type Severity string

// The two diagnostic severities defined by D13.
const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Code is a stable machine-matchable diagnostic identifier (D13). Codes are
// part of the agent-facing contract: renaming one is a breaking change.
type Code string

// The diagnostic codes liquid build and liquid vet can emit.
const (
	// CodeMalformedTemplate reports .lsx syntax the compiler cannot parse,
	// such as an interpolation opened with {{ and never closed.
	CodeMalformedTemplate Code = "LSX001"
	// CodeMissingPairedSource reports a .lsx file with no paired .go source
	// file next to it.
	CodeMissingPairedSource Code = "LSX002"
	// CodeMissingPairedStruct reports a paired .go file that does not define
	// the struct the filename convention requires.
	CodeMissingPairedStruct Code = "LSX003"
	// CodeUnknownReference reports a template expression referencing a field
	// or method that does not exist on the paired struct.
	CodeUnknownReference Code = "LSX004"
	// CodeMalformedDirective reports a structural directive whose expression
	// does not match the directive's grammar.
	CodeMalformedDirective Code = "LSX005"
	// CodeConflictingDirectives reports an element carrying more than one
	// structural directive.
	CodeConflictingDirectives Code = "LSX006"
	// CodeBrokenPairedPackage reports a paired Go package that fails
	// type-checking; the position points into the offending Go source.
	CodeBrokenPairedPackage Code = "LSX007"
	// CodeInvalidHandler reports an event binding whose target exists but is
	// not a dispatchable handler method (D11).
	CodeInvalidHandler Code = "LSX008"
	// CodeMissingHydroField reports a [hydroId] declaration on a component
	// whose struct lacks the HydroID string field the framework fills.
	CodeMissingHydroField Code = "LSX009"
	// CodeMissingHydroRoot reports an event binding in a template that never
	// declares a [hydroId] patch boundary.
	CodeMissingHydroRoot Code = "LSX010"
	// CodeMissingCSRFField reports a template with a <form> on a component
	// whose struct lacks the CSRFToken string field the framework fills (D15).
	CodeMissingCSRFField Code = "LSX011"
	// CodeUnknownChildSelector reports a child-selector element whose tag no
	// component in the package declares (D14).
	CodeUnknownChildSelector Code = "LSX012"
	// CodeBadInputBinding reports an [input] binding that cannot copy at
	// render: a malformed expression, a child field that does not exist, or
	// a parent value not assignable to the child field.
	CodeBadInputBinding Code = "LSX013"
	// CodeChildBindingUnsupported reports an event binding or [hydroId] on a
	// child-selector element, which the child's render replaces wholesale —
	// such bindings belong inside the child's own template.
	CodeChildBindingUnsupported Code = "LSX014"
	// CodeMisplacedDefer reports *liquidDefer off a child-selector element:
	// only a nested component occurrence can render deferred (D14).
	CodeMisplacedDefer Code = "LSX015"
	// CodeMissingDeferHydroField reports *liquidDefer on a component whose
	// struct lacks the HydroID string field — without it the completion
	// patch has no boundary to swap at.
	CodeMissingDeferHydroField Code = "LSX016"
	// CodeUnmanagedSubscription reports a direct Subscribe call on a Liquid
	// observable (BehaviorSubject, Derived, or the Observable interface) in
	// component code, whose lifecycle is not owned by the framework (D25/D29).
	// A bare call that discards its cancel is a provable leak (error); a call
	// whose cancel is captured but not session-bound is a warning. The managed
	// path is liquid.Observe within Subscriptions(). Suppress a deliberate
	// direct subscription with a //liquid:allow-subscribe comment.
	CodeUnmanagedSubscription Code = "LSX017"
)

// Diagnostic is one structured compiler finding: the literal contract an
// agent parses to self-repair (D13), so the field set and JSON names are the
// API. Line and Col are 1-based; Col counts bytes.
type Diagnostic struct {
	File       string   `json:"file"`
	Line       int      `json:"line"`
	Col        int      `json:"col"`
	Severity   Severity `json:"severity"`
	Code       Code     `json:"code"`
	Message    string   `json:"message"`
	Suggestion string   `json:"suggestion"`
}
