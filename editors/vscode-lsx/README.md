# Liquid `.lsx` for VS Code

Syntax highlighting for Liquid component templates (`.lsx`): standard HTML
plus the Liquid sugar from
[template-syntax.md](../../docs/template-syntax.md):

- `{{ Field }}` / `{{ $var }}` interpolation — in text *and* attribute values
- `*goIf` / `*goFor` (and future `*go…`) structural directives, including the
  `let item of Items` grammar inside `*goFor`
- `(click)` / `(submit)` (and future `(event)`) bindings, with the handler
  name scoped as a function reference
- `[hydroId]` (and future `[binding]`) markers

Patterns are shape-generic (`*Name`, `(name)`, `[name]`), so new directives
highlight without touching the grammar. Directive names are matched
case-insensitively nowhere — but the compiler treats spellings
case-insensitively, and the generic patterns accept any casing anyway.

## Install

No build step — VS Code loads the grammar straight from this folder.

**Option A — symlink (dev install):**

```sh
ln -s "$(pwd)/editors/vscode-lsx" ~/.vscode/extensions/liquid-lsx-0.1.0
```

Then restart VS Code (or run *Developer: Reload Window*).

**Option B — packaged VSIX:**

```sh
cd editors/vscode-lsx
npx @vscode/vsce package        # emits liquid-lsx-0.1.0.vsix
code --install-extension liquid-lsx-0.1.0.vsix
```

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
