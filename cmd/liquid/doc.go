// Command liquid is the Liquid CLI: the ahead-of-time (AOT) template compiler,
// the component scaffolder, the dev server, and the editor language server.
//
// Usage:
//
//	liquid <build|vet|manifest|generate|dev|lsp> [args]
//
// The verbs:
//
//   - build    Compile each component's .lsx template into Go ahead of time,
//     so templates are parsed and validated at build time rather than
//     on the first request.
//   - vet      Run the same compilation checks as build without emitting code,
//     reporting problems and exiting non-zero when any are found.
//   - manifest Emit the machine-readable manifest of components and actions
//     that the other verbs and editor tooling consume.
//   - generate Scaffold a new component (its Go type and paired .lsx file).
//   - dev      Run the local development server, rebuilding on change.
//   - lsp      Serve the editor language server for .lsx files.
//
// # AOT compilation as an agent guardrail
//
// Compiling templates ahead of time is what lets Liquid stay safe under
// machine-generated code. The compiler works on a parsed HTML node tree, not
// line- or regex-based text, and it resolves every event binding against a
// compile-time action allowlist. An action an agent invents that no handler
// exports fails the build instead of reaching runtime reflection; markup that
// would break contextual escaping is caught before it ships. build and vet
// give an agent a fast, deterministic oracle: the code either compiles against
// these rules or it does not, so a whole class of injection and dispatch
// mistakes is turned into a build error rather than a production defect.
package main
