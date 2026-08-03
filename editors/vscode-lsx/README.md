# Liquid `.lsx` for VS Code

Language support for Liquid component templates (`.lsx`), covering the
syntax from [template-syntax.md](../../docs/template-syntax.md).

**Syntax highlighting** (grammar-only, works with zero setup):

- `{{ Field }}` / `{{ $var }}` interpolation — in text *and* attribute values
- `*goIf` / `*goFor` (and future `*go…`) structural directives, including the
  `let item of Items` grammar inside `*goFor`
- `(click)` / `(submit)` (and future `(event)`) bindings, with the handler
  name scoped as a function reference
- `[hydroId]` (and future `[binding]`) markers

**Language intelligence** (needs the `liquid` CLI — the extension runs
`liquid lsp` and speaks LSP to it):

- **Live diagnostics** — the same LSX001–LSX014 findings `liquid vet`
  reports, as you type, with the compiler's "did you mean" suggestions.
- **Hover** — field/method types and Go doc comments under `{{ … }}` and
  directive expressions; directive documentation on `*goIf`, `(click)`,
  `[hydroId]`, …; loop variables; child-component selectors.
- **Completion** — struct members inside `{{ }}` and directive expressions,
  dispatchable handlers only inside `(click)=` / `(submit)=`, directive
  attribute names, `[input]` fields on child elements, and child-component
  selectors as tag names.
- **Go to definition** (ctrl/cmd+click) — from a template reference to the
  Go field, method, or component struct that backs it.
- **Occurrence highlights** — every use of the symbol under the cursor
  across the template's expressions.

The server reuses the compiler's own scanner and `go/types` cross-check, so
what the editor says and what `liquid vet` says can never disagree.

## Install

```sh
cd editors/vscode-lsx
npm install          # pulls vscode-languageclient for the LSP client
```

**Option A — symlink (dev install):**

```sh
ln -s "$(pwd)" ~/.vscode/extensions/liquid-lsx-0.2.0
```

Then restart VS Code (or run *Developer: Reload Window*).

**Option B — packaged VSIX:**

```sh
npx @vscode/vsce package        # emits liquid-lsx-0.2.0.vsix
code --install-extension liquid-lsx-0.2.0.vsix
```

The language server is the `liquid` binary itself:

```sh
go install github.com/rmoralesthompson/liquid/cmd/liquid@latest
```

If `liquid` is not on VS Code's PATH, set `liquid.lsx.serverPath` to its
location. Without the binary (or without `npm install`) the extension
degrades to grammar-only syntax highlighting.

Open `demo.lsx` in this folder to eyeball every construct at once; the
*Developer: Inspect Editor Tokens and Scopes* command shows the scopes the
grammar assigns.

## How it works

- `syntaxes/lsx.tmLanguage.json` — the `lsx` language is HTML
  (`text.html.basic`) at its core.
- `syntaxes/lsx-expression.injection.tmLanguage.json` — injects `{{ … }}`
  highlighting everywhere in the document except comments and embedded
  script/style, which is what makes interpolation inside attribute strings
  work.
- `syntaxes/lsx-attributes.injection.tmLanguage.json` — injects the
  `*directive` / `(event)` / `[marker]` attribute shapes inside tags,
  ahead of the stock HTML attribute rules.
- `client/extension.js` — a thin `vscode-languageclient` shim that spawns
  `liquid lsp` over stdio. All analysis lives in the Go server
  (`cmd/liquid/internal/lsp`), which delegates to the compiler package.
