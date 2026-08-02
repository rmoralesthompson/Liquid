package liquid

import (
	"fmt"
	"reflect"
	"strings"
)

// injection is one resolved dependency: which struct field receives which
// singleton. Resolution happens once, at route registration; requests only
// copy the resolved value into the fresh instance.
type injection struct {
	field int
	svc   reflect.Value
}

// Provide registers svc as an app-lifetime singleton available for injection
// into component fields tagged `inject:""` (D8). Provide services before
// registering the routes that need them: resolution happens at Route, and an
// unresolvable dependency fails registration. A service is shared across
// requests and must be safe for concurrent use.
func (a *App) Provide(svc any) error {
	v := reflect.ValueOf(svc)
	if !v.IsValid() {
		return fmt.Errorf("providing service: got nil")
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func:
		if v.IsNil() {
			return fmt.Errorf("providing service: got a nil %s; it would inject as a nil field", v.Type())
		}
	default:
	}
	a.services = append(a.services, v)
	return nil
}

// resolveInjections builds the injection plan for a component type: every
// `inject:""` field paired with the provided singleton that satisfies it. Any
// unsatisfiable field is a hard registration error — never a nil field
// discovered at request time (D8).
func (a *App) resolveInjections(t reflect.Type) ([]injection, error) {
	var plan []injection
	for i := range t.NumField() {
		f := t.Field(i)
		if _, ok := f.Tag.Lookup("inject"); !ok {
			continue
		}
		if !f.IsExported() {
			return nil, fmt.Errorf("field %s.%s: inject requires an exported field", t.Name(), f.Name)
		}
		svc, err := a.resolveService(f.Type)
		if err != nil {
			return nil, fmt.Errorf("field %s.%s: %w", t.Name(), f.Name, err)
		}
		plan = append(plan, injection{field: i, svc: svc})
	}
	return plan, nil
}

// resolveService finds the single provided service satisfying the field
// type: an exact concrete-type match, or — for interface fields — a service
// implementing the interface. Zero or several candidates are both hard
// errors; ambiguity is never resolved by a silent pick.
func (a *App) resolveService(ft reflect.Type) (reflect.Value, error) {
	var matches []reflect.Value
	for _, s := range a.services {
		if s.Type() == ft || (ft.Kind() == reflect.Interface && s.Type().Implements(ft)) {
			matches = append(matches, s)
		}
	}
	switch len(matches) {
	case 0:
		return reflect.Value{}, fmt.Errorf("no provided service satisfies type %s", ft)
	case 1:
		return matches[0], nil
	}
	names := make([]string, len(matches))
	for i, m := range matches {
		names[i] = m.Type().String()
	}
	return reflect.Value{}, fmt.Errorf("%d provided services satisfy type %s (%s); dependencies must resolve to exactly one", len(matches), ft, strings.Join(names, ", "))
}
