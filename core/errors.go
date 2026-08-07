package liquid

// This file is the form-validation surface (#105, ADR-0004). A typed-payload
// handler's parameter may implement Validator; when it does, the dispatch seam
// runs Validate() after binding and before the handler, and on a non-empty
// result re-renders the component with the errors instead of calling the
// handler. The errors reach the template through a component field of type
// Errors, which the framework populates before the re-render.

import "reflect"

// FieldError is one validation failure: the payload field it concerns and a
// human-readable message. A field with no natural name (a form-wide error) may
// use the empty string.
type FieldError struct {
	Field   string
	Message string
}

// Errors is an ordered collection of per-field validation errors. A typed
// payload's Validate method returns one; a component renders it through a field
// of this type, which the framework fills on a failed submit and clears on a
// successful dispatch. The zero value is an empty, ready-to-use set.
type Errors struct {
	items []FieldError
}

// Add appends a validation error for field with message. Add has a pointer
// receiver, so build errors on an addressable value — the idiomatic
// `var errs liquid.Errors; errs.Add(...)` inside a Validate method.
func (e *Errors) Add(field, message string) {
	e.items = append(e.items, FieldError{Field: field, Message: message})
}

// Any reports whether the set holds any error — the template guard
// `{{ if .Errors.Any }}`.
func (e Errors) Any() bool { return len(e.items) > 0 }

// Len returns the number of errors.
func (e Errors) Len() int { return len(e.items) }

// All returns every error in insertion order, for iterating the whole set.
func (e Errors) All() []FieldError { return e.items }

// For returns the messages recorded for a field, in order, or nil when it has
// none — the per-field template accessor
// `{{ range .Errors.For "Email" }}<p>{{ . }}</p>{{ end }}`.
func (e Errors) For(field string) []string {
	var msgs []string
	for _, it := range e.items {
		if it.Field == field {
			msgs = append(msgs, it.Message)
		}
	}
	return msgs
}

// Validator is implemented by a typed payload that wants server-side validation
// (#105). The seam calls Validate after binding the wire payload and before the
// handler; a non-empty result skips the handler and re-renders with the errors.
// Validate is arbitrary Go over the bound value — the framework prescribes no
// rule DSL (consistent with D30's Go-predicate guards).
type Validator interface {
	Validate() Errors
}

// errorsType is the reflect shape of the Errors field the framework populates on
// a component for the re-render after a failed validation.
var errorsType = reflect.TypeFor[Errors]()

// setFormErrors writes errs into the component's first settable field of type
// Errors, so its template can render them on the next render. It is a no-op when
// the component declares no such field (validation still gated dispatch; the
// component simply has nowhere to show the errors). inst is the *T instance
// value the registry holds.
func setFormErrors(inst reflect.Value, errs Errors) {
	s := inst.Elem()
	for i := range s.NumField() {
		f := s.Field(i)
		if f.Type() == errorsType && f.CanSet() {
			f.Set(reflect.ValueOf(errs))
			return
		}
	}
}
