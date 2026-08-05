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
var Corpus = []Task{GreenfieldCounter}

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
