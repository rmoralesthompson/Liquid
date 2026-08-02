package liquid

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// Event is the payload handed to a func(e liquid.Event) handler (D11): typed
// accessors over the fields the runtime script posted with the event — for a
// (submit), the serialized form — plus the request Ctx (D18) through
// embedding. Handlers needing no payload stay func().
type Event struct {
	Ctx
	fields map[string]string
	reply  *eventReply
}

// eventReply collects what a handler answers beyond mutating its component.
// Event is passed by value (D11), so the reply lives behind a pointer the
// dispatcher reads after the handler returns.
type eventReply struct {
	redirect string
}

// Redirect answers the event with a client-side navigation to path instead
// of an HTML patch (D19). The last call wins; state mutated before the call
// still happens, it just isn't re-rendered.
func (e Event) Redirect(path string) {
	if e.reply != nil {
		e.reply.redirect = path
	}
}

// String returns the named payload field, or "" when the event carries none.
func (e Event) String(name string) string { return e.fields[name] }

// Int returns the named payload field parsed as an integer, or 0 when the
// field is absent or not a number; use Bind to surface parse failures.
func (e Event) Int(name string) int {
	n, err := strconv.Atoi(e.fields[name])
	if err != nil {
		return 0
	}
	return n
}

// Bind fills dst — a pointer to a struct — from the event's payload. Each
// payload field is matched to an exported struct field by case-insensitive
// name; matched string fields copy verbatim and int fields parse, with a
// parse failure reported as an error naming the payload field. Payload
// fields without a matching struct field (the auto-injected csrf_token, say)
// are ignored, as are struct fields the payload does not mention.
func (e Event) Bind(dst any) error {
	v := reflect.ValueOf(dst)
	if v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("liquid: Bind wants a non-nil pointer to a struct, got %T", dst)
	}
	structV := v.Elem()
	t := structV.Type()
	for name, value := range e.fields {
		f, ok := fieldByFoldedName(t, name)
		if !ok {
			continue
		}
		target := structV.FieldByIndex(f.Index)
		switch f.Type.Kind() {
		case reflect.String:
			target.SetString(value)
		case reflect.Int:
			n, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("liquid: binding field %s: %q is not an integer", name, value)
			}
			target.SetInt(int64(n))
		default:
			return fmt.Errorf("liquid: binding field %s: %s fields are not bindable in v0.1 (string and int are)",
				name, f.Type.Kind())
		}
	}
	return nil
}

// fieldByFoldedName finds the exported struct field matching name under
// case-insensitive comparison.
func fieldByFoldedName(t reflect.Type, name string) (reflect.StructField, bool) {
	for i := range t.NumField() {
		f := t.Field(i)
		if f.IsExported() && strings.EqualFold(f.Name, name) {
			return f, true
		}
	}
	return reflect.StructField{}, false
}
