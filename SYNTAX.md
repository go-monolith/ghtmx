# The `.ghtmx` Syntax Specification

This document is the language surface's source of truth, and the tests
are its enforcement: every construct below carries *Verified by*
anchors naming the corpus entries and example suites that run it on
every build. `internal/syntaxcheck` keeps the contract bidirectional —
every golden corpus entry must be referenced here, every referenced
path must exist and contain tests, and every parser node type must be
documented. **Undocumented syntax is unsupported**: a new construct
fails the coverage check until this document describes it.

The surface is a syntactic superset of templ at the pinned
`TEMPL_SYNTAX_BASELINE` — commit
`04abee5364c6fab2bde8c00d215fdcb630ad6a94` (≈ v0.3.1020+36), see
`TEMPL_SYNTAX_BASELINE.md` — with two semantic carve-outs, itemized
with their replacement constructs in the final section and, in full,
in `CONFORMANCE.md`.

## File structure

A `.ghtmx` file is a Go file that may additionally contain `templ`,
`fragment`, `event`, `css`, and `script` declarations. Ordinary Go —
the package clause, imports, functions, types, variables — passes
through to the generated file verbatim.

```templ
package main

import "strings"

var greeting = "Hello"

templ page(name string) {
	<p>{ greeting + ", " + strings.TrimSpace(name) }</p>
}
```

AST: `TemplateFile` holds `Package` and a list of `TemplateFileNode`s;
Go passthrough is `TemplateFileGoExpression`.

*Verified by:* `internal/generator/test-import`,
`internal/generator/test-go-comments`, `internal/parser/`.

## Template declarations

`templ Name(params) { … }` declares a component returning
`ghtmx.Component`. Methods (`templ (r Receiver) Name() { … }`) and
type parameters are supported.

```templ
templ hello(name string) {
	<div>Hello, { name }</div>
}
```

AST: `HTMLTemplate`; its body is a `Nodes` list of `Node`s
(`CompositeNode` for the block-shaped ones).

*Verified by:* `internal/generator/test-html`,
`internal/generator/test-method`.

## Text and interpolation

Literal text renders as-is (HTML-escaped where dynamic). `{ expr }`
interpolates any Go expression yielding a string or basic Go type,
context-escaped (see `conformance/`). `{{ statements }}` runs Go
statements inside a body without rendering. Whitespace between nodes
is preserved per the baseline's rules.

```templ
templ text(count int) {
	{{ label := strconv.Itoa(count) }}
	Plain text, { label } items
}
```

AST: `Text`, `StringExpression`, `Whitespace`, `WhitespaceTrailer`,
`GoCode` (the `{{ statements }}` form; `internal/parser/` covers its
grammar in gocodeparser tests).

*Verified by:* `internal/generator/test-text`,
`internal/generator/test-string`,
`internal/generator/test-string-errors`,
`internal/generator/test-text-inline-expression`,
`internal/generator/test-text-whitespace`,
`internal/generator/test-primitives`,
`internal/generator/test-whitespace-around-go-keywords`.

## Elements

Standard HTML elements with nesting, void elements
(`<input/>`, `<br/>`), raw-text elements whose content is not parsed
as ghtmx (`<style>`; `<script>` is handled separately with JS-aware
lexing — see the Script section), and doctypes.

```templ
templ shell() {
	<!DOCTYPE html>
	<div>
		<input type="text" name="q"/>
	</div>
}
```

AST: `Element`, `RawElement`, `DocType`.

*Verified by:* `internal/generator/test-html`,
`internal/generator/test-void`,
`internal/generator/test-raw-elements`,
`internal/generator/test-doctype`,
`internal/generator/test-doctype-html4`.

## Attributes

Six attribute forms, all inherited from the baseline:

```templ
templ attrs(href string, hidden bool, extra ghtmx.Attributes) {
	<a
		id="constant"
		href={ ghtmx.URL(href) }
		disabled?={ hidden }
		download
		if hidden {
			aria-hidden="true"
		}
		{ "data-" + "computed" }={ "dynamic key" }
		{ extra... }
	>link</a>
}
```

- constant (`ConstantAttribute`) and boolean-constant
  (`BoolConstantAttribute`),
- expression value (`ExpressionAttribute`) and boolean-expression
  (`BoolExpressionAttribute`, the `?=` form),
- conditional blocks inside the tag (`ConditionalAttribute`),
- spreads (`SpreadAttributes`, `{ attrs... }`).

Attribute names may themselves be dynamic (`AttributeKey`,
`ConstantAttributeKey`, `ExpressionAttributeKey`), comments may sit
between attributes (`AttributeComment`), `class` values compose from
strings and `css` templates, and `style` values accept strings
(breakout-escaped) or maps (property-value sanitized) — see
`conformance/` for the escaping contract. The `Attribute` interface is
the AST umbrella for all of these.

*Verified by:* `internal/generator/test-element-attributes`,
`internal/generator/test-complex-attributes`,
`internal/generator/test-attribute-escaping`,
`internal/generator/test-constant-attribute-escaping`,
`internal/generator/test-attribute-comments`,
`internal/generator/test-attribute-errors`,
`internal/generator/test-spread-attributes`,
`internal/generator/test-class-whitespace`,
`internal/generator/test-style-attribute`,
`internal/generator/test-form-action`,
`internal/generator/test-a-href`.

## Control flow

Go-shaped `if`/`else if`/`else`, `switch` with `case`/`default` (and
`fallthrough`), and `for` — bodies are template content.

```templ
templ list(items []string) {
	if len(items) == 0 {
		<p>empty</p>
	} else {
		<ul>
			for _, item := range items {
				<li>{ item }</li>
			}
		</ul>
	}
}
```

AST: `IfExpression`, `ElseIfExpression`, `SwitchExpression`,
`CaseExpression`, `Fallthrough`, `ForExpression`.

*Verified by:* `internal/generator/test-if`,
`internal/generator/test-ifelse`, `internal/generator/test-elseif`,
`internal/generator/test-switch`,
`internal/generator/test-switchdefault`,
`internal/generator/test-for`.

## Components and children

`@Name(args)` renders another component; a trailing block passes
children, rendered where the callee says `{ children... }`. The legacy
`{! expr }` call form from the baseline is retained.

```templ
templ layout() {
	<main>{ children... }</main>
}

templ page() {
	@layout() {
		<p>body</p>
	}
}
```

AST: `TemplElementExpression`, `ChildrenExpression`,
`CallTemplateExpression` (legacy form).

*Verified by:* `internal/generator/test-call`,
`internal/generator/test-templ-element`,
`internal/generator/test-go-template-in-templ`,
`internal/generator/test-templ-in-go-template`,
`internal/generator/test-context`,
`internal/generator/test-cancelled-context`.

## CSS templates

`css name(params) { … }` declares a scoped class; property values may
be constant or `{ expr }` (sanitized). Use via `class={ name(...) }`.

```templ
css warning(color string) {
	background-color: #ffeeee;
	color: { color };
}
```

AST: `CSSTemplate`, `CSSProperty`, `ConstantCSSProperty`,
`ExpressionCSSProperty`.

*Verified by:* `internal/generator/test-css-expression`,
`internal/generator/test-css-usage`,
`internal/generator/test-css-middleware`, `conformance/`.

## Script

`script name(params) { … }` declares a JS function template;
`<script>` element bodies may interpolate `{{ expr }}` with JS-context
escaping; `ghtmx.JSFuncCall` builds escaped handler attributes;
`ghtmx.JSONScript` emits JSON data islands; once-handles deduplicate
shared snippets.

```templ
script alertName(name string) {
	alert(name);
}

templ buttons(msg string) {
	<button onClick={ alertName(msg) }>go</button>
	<script>
		var payload = "{{ msg }}";
	</script>
}
```

AST: `ScriptTemplate`, `ScriptElement`, `ScriptContents`.

*Verified by:* `internal/generator/test-script-usage`,
`internal/generator/test-script-inline`,
`internal/generator/test-script-expressions`,
`internal/generator/test-script-usage-nonce`,
`internal/generator/test-only-scripts`,
`internal/generator/test-js-usage`,
`internal/generator/test-js-unsafe-usage`,
`internal/generator/test-once`.

## Comments

Go comments (`//`, `/* */`) inside code islands and template bodies;
HTML comments (`<!-- -->`) render into the output.

AST: `GoComment`, `HTMLComment`.

*Verified by:* `internal/generator/test-go-comments`,
`internal/generator/test-html-comment`.

## Fragments (ghtmx)

`fragment Name(params) { … }` declares a compile-time fragment —
nested inside a template it renders inline at the declaration site
with arguments bound by name from the enclosing scope, top-level it
only declares. Each fragment compiles to one shared body with two
entry points: `Name(...) ghtmx.Component` (inline) and
`NameFragment(...) ghtmx.Fragment` (standalone), byte-identical for
identical inputs (FR-031). Fragment bodies see only their declared
parameters.

```templ
templ userTable(users []User) {
	<table>
		for _, u := range users {
			fragment userRow(u User) {
				<tr><td>{ u.Name }</td></tr>
			}
		}
	</table>
}
```

AST: `FragmentDeclaration`.

*Verified by:* `examples/fragments/`, `examples/crud/`,
`conformance/`, `internal/parser/`, `internal/analyzer/`.

## Events (ghtmx)

`event Name(typed params)` declares a server-driven event
(FR-036/FR-037). The kebab-case wire name (`user-created`) is the only
listener token the compiler owns in `hx-on:*`/`hx-on-*` suffixes and
`hx-trigger` values (the `::`/`--`/`-htmx-` shorthands stay in htmx's
own namespace); declarations generate the only `HX-Trigger` emission
symbols (`ghtmxgen.EmitName*`).

```templ
event ItemSaved(id string)

templ panel() {
	<div hx-on:item-saved="refresh()">…</div>
}
```

AST: `EventDeclaration`.

*Verified by:* `examples/events/`, `examples/crud/`, `conformance/`,
`internal/analyzer/`.

## Route bindings (ghtmx)

The five verb attributes accept a handler symbol
(`hx-post={ handlers.CreateUser }`, zero-parameter routes, path folded
statically) or a generated constructor
(`hx-get={ ghtmxgen.GetUser(id) }`, parameters percent-encoded),
resolved at build time against routes discovered from Go source
(FR-020/FR-021). Registrations the discoverer cannot resolve use the
escape hatch: `//ghtmx:route GET /admin/users/{id} handlers.Show`, whose
symbol may also name a method — `Handlers.Show` or `pkg.Handlers.Show`.
Handlers registered as method values (`r.Get("/users", h.ListUsers)`) are
discovered whenever the receiver's type is named in the same function.
A method is not a symbol a template can name, so these routes bind
through their generated central symbols, which fold the dot away:
`hx-get={ ghtmxgen.HandlersListUsersPath }` for a route without
parameters, `hx-get={ ghtmxgen.HandlersGetUser(id) }` for one with.
A package whose routes are served under a mount prefix declares it once
with `//ghtmx:routeprefix /admin/user` — a sub-application mounted at a
variable prefix cannot be recognised syntactically, so every route the
package registers, discovered or annotated, is composed under the
declared prefix (FR-013). The prefix must be static; one per package.
Annotated paths are relative to it, so adopting the directive means
shortening annotations that already spell the mount point — an
annotation left absolute composes twice.
An annotation may carry a trailing `nav` marker
(`//ghtmx:route GET /audit handlers.AuditLog nav`) declaring a
navigation-only route — reached by `<a href>` or a native form post —
which exempts it from the `GHTMX-W0104` unbound-route warning.
Other `hx-*` attributes are validated against the pinned htmx
version's surface (`internal/htmxsurface`).

*Verified by:* `examples/hx-bindings/`, `examples/crud/`,
`internal/analyzer/`, `internal/routes/`, `conformance/`.

## AST umbrella types

`Node`, `Nodes`, `CompositeNode`, `TemplateFileNode`, `Attribute`, and
`AttributeKey` are the parser's interface and container shapes; every
concrete construct above implements one of them. They admit no syntax
of their own.

## Carve-outs and corpus exclusions (FR-004)

The two semantic carve-outs from the baseline, each with its
replacement construct — itemized case-by-case in `CONFORMANCE.md`:

1. **`hx-*` verb attributes are typed bindings.** A string URL is
   `GHTMX-E0602`, an arbitrary expression `GHTMX-E0601`; the
   replacements are the handler-symbol and constructor binding forms
   above.
2. **URL escaping at binding sites is engine-determined.** An explicit
   `ghtmx.URL`/`SafeURL` call at a binding site is `GHTMX-E0603`; the
   replacement is the `ghtmx.SafeURL`-returning generated constructor.
   (`ghtmx.URL` for `href`/`action` and friends is inherited
   unchanged.)

Non-carve-out corpus exclusions, each documented with its reason and
replacement in `CONFORMANCE.md`'s exclusion list: the baseline's
runtime-fragment API (its `test-fragment` golden dir) is removed in
favor of `fragment` declarations, and the baseline's
Prettier-dependent formatter and HTML-diff behavior is replaced with
verbatim pass-through and structural comparison.
