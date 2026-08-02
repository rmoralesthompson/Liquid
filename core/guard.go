package liquid

// Guard decides whether a request may activate a route (CanActivate, D4).
// Guards run after route matching and before the component is instantiated.
type Guard func(ctx Ctx) GuardResult

// GuardResult is a guard's verdict: allow, deny, or redirect (D19).
type GuardResult struct {
	allowed    bool
	redirectTo string
}

// Allow lets the request proceed to the component.
func Allow() GuardResult { return GuardResult{allowed: true} }

// Deny blocks the request with 403 Forbidden.
func Deny() GuardResult { return GuardResult{} }

// Redirect blocks the request and sends the client to path instead — the
// login-flow variant of a denial (D19).
func Redirect(path string) GuardResult { return GuardResult{redirectTo: path} }
