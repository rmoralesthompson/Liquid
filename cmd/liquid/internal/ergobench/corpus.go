package ergobench

// This file seeds the Tier B task corpus. ADR-0001 targets ~8–10 tasks grown
// from real usage and tiered by the agent skill each exercises (greenfield,
// add interactivity, [input] wiring, managed Observe, repair-only, and a
// guardrail "trap"). It starts with the greenfield skill; the rest land with
// the live generator (Layer 2), each as {prompt, expected structure, cap}. The
// corpus is the bias — it encodes our assumptions about what agents do — so it
// is kept small, explicit, and marked as sampled rather than exhaustive.

// boolPtr returns a pointer to b, for the optional (nil = don't-assert) fields
// of an Expectation.
func boolPtr(b bool) *bool { return &b }

// Corpus is the ordered set of Tier B tasks the live generator scores against.
// It samples four distinct agent skills — greenfield interactivity, parent→child
// [input] wiring, the framework-managed Observe path, and being steered off a
// guardrail (raw Subscribe) onto that path — and is marked sampled, not
// exhaustive (ADR-0001, "the corpus is the bias").
var Corpus = []Task{GreenfieldCounter, InputWiring, ManagedObserve, GuardrailTrap}

// GreenfieldCounter exercises the greenfield-with-interactivity skill: emit a
// working interactive component from scratch, with one action wired to a click.
// Its expectation is grounded in the manifest an equivalent hand-written
// component actually produces.
var GreenfieldCounter = Task{
	Name:   "greenfield-counter",
	Prompt: "Create a Liquid component with selector app-counter (struct Counter) that displays an int field Count and has an Increment action wired to a button's (click). Give it a [hydroId] interactive root.",
	CapN:   3,
	Expect: Expectation{
		Selector:    "app-counter",
		Interactive: boolPtr(true),
		Fields: []FieldExpect{
			{Name: "Count", Type: "int"},
			{Name: "HydroID", Type: "string"},
		},
		Actions: []ActionExpect{
			{Name: "Increment", TakesEvent: boolPtr(false), Events: []string{"click"}},
		},
	},
}

// InputWiring exercises the parent→child [input] skill: a parent that nests a
// child and passes a value down through a [name] input binding. The child
// field's manifest input flag is the oracle — it is true only when some
// template actually binds the field via [input], so a forgotten binding fails
// spec-match even though both components still compile clean.
var InputWiring = Task{
	Name:   "input-wiring",
	Prompt: "Create two Liquid components. A parent with selector app-dashboard (struct Dashboard, string field Owner) whose template nests the child and passes Owner down to it. A child with selector app-user-card (struct UserCard, string field Name) that displays Name. Wire the parent to the child with a [name] input binding so the child receives Owner as its Name.",
	CapN:   3,
	Expect: Expectation{
		Selector: "app-user-card",
		Fields: []FieldExpect{
			{Name: "Name", Type: "string", Input: boolPtr(true)},
		},
	},
}

// ManagedObserve exercises the managed-subscription skill: follow an observable
// through the framework-owned Observe path inside Subscriptions(), so the D29
// leak check leaves it alone and the component vets clean. It imports liquid
// core, so its scaffold gets the stub (NeedsCore).
var ManagedObserve = Task{
	Name:      "managed-observe",
	Prompt:    "Create an interactive Liquid component with selector app-managed (struct Managed) and a [hydroId] root. It has an int field Count that mirrors an observable of int. Follow the observable with the framework-managed liquid.Observe inside a Subscriptions() method so the framework owns the subscription lifecycle. Do not call Subscribe directly.",
	CapN:      3,
	NeedsCore: true,
	Expect: Expectation{
		Selector:    "app-managed",
		Interactive: boolPtr(true),
		Fields: []FieldExpect{
			{Name: "Count", Type: "int"},
		},
	},
}

// GuardrailTrap exercises the guardrail-steering skill: the obvious way to
// follow an observable — call Subscribe in OnInit — trips the D29 leak check
// (LSX017), whose suggestion steers the fix onto the managed Observe path. The
// task scores whether the loop reaches green after being redirected; its prompt
// deliberately never names Observe, so a first-pass-clean result means the
// generator avoided the trap unprompted. It imports liquid core (NeedsCore).
var GuardrailTrap = Task{
	Name:      "guardrail-trap",
	Prompt:    "Create an interactive Liquid component with selector app-live (struct Live) and a [hydroId] root. It has an int field Count that always reflects the latest value of an observable of int. Keep Count in sync with the observable.",
	CapN:      3,
	NeedsCore: true,
	Expect: Expectation{
		Selector:    "app-live",
		Interactive: boolPtr(true),
		Fields: []FieldExpect{
			{Name: "Count", Type: "int"},
		},
	},
}
