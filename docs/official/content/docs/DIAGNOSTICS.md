# Diagnostic Catalogue

Every diagnostic the toolchain emits, by stable ID (FR-045). IDs never
change meaning; `internal/catalogcheck` fails the build if an ID exists
in `internal/diag/ids.go` without an entry here, or here without
existing there. Severity is the default: warning-class checks can be
re-tuned per project via the `checks` setting (see `CONFIG.md`);
error-class checks cannot be silenced.

ID families: `E01xx` route bindings, `E02xx` attributes, `E03xx`
declarations, `E04xx` route discovery, `E05xx` htmx versions, `E06xx`
templ carve-outs, `W01xx` reachability, `W02xx` targets, `W03xx` build
hygiene.

## Errors

| ID | Cause | Remedy |
| --- | --- | --- |
| `GHTMX-E0101` | A verb attribute binds a handler symbol or constructor no discovered route registers — or the symbol's package is not imported by the template file. | Register the route in Go code within `routeScope`, fix the symbol, add the missing import, or declare the route with a `//ghtmx:route` annotation. |
| `GHTMX-E0102` | A constructor binding's route verb disagrees with the attribute (`hx-get={ ghtmxgen.CreateUser(...) }` on a POST route). | Use the verb attribute matching the route, or bind the right constructor. |
| `GHTMX-E0103` | A bare handler-symbol binding names a parameterised route, which needs arguments. | Bind through the generated constructor: `hx-get={ ghtmxgen.GetUser(id) }`. |
| `GHTMX-E0104` | A constructor call's argument count differs from the route's parameters. | Match the constructor signature; run `ghtmx routes` (or `ghtmx routes -json` for scripting) to see it. |
| `GHTMX-E0201` | An `hx-*` attribute name is unknown to the pinned htmx version. | Fix the typo (the message suggests the nearest name) or adjust `htmxVersion`. |
| `GHTMX-E0202` | A constant `hx-*` attribute value is invalid for its attribute grammar. | Use a value the pinned htmx version's grammar accepts; interpolated values are exempt. |
| `GHTMX-E0203` | Two attributes on one element conflict per the htmx surface rules. | Remove one of the conflicting attributes. |
| `GHTMX-E0301` | Two fragments in one package share a name. | Rename one; fragment names are unique per package. |
| `GHTMX-E0302` | A fragment body uses `{ children... }`. | Fragments take no children; pass data through parameters instead. |
| `GHTMX-E0303` | A fragment reference cannot resolve: wrong arity, not visible cross-package, or an uncalled fragment reference. (Unknown names are ordinary component references and surface as `undefined: X` from the Go compiler.) | Fix the arguments, export the fragment, or call it as `@Name(args)`. |
| `GHTMX-E0304` | An `hx-on:*`/`hx-on-*` listener or `hx-trigger` token uses a compiler-owned wire name no `event` declares. | Declare it with `event Name(...)`, or use a `:`-qualified name for events outside the contract. |
| `GHTMX-E0305` | Two `event` declarations collide on a wire name (event names are global to the compiled set). | Rename one declaration. |
| `GHTMX-E0306` | Fragment or template references form a cycle. | Break the cycle; the message lists the full chain. |
| `GHTMX-E0307` | A template and another template or fragment in the package share a name. | Rename one declaration. |
| `GHTMX-E0401` | Two registrations claim the same verb and path. | Remove or change one registration; the message names both sites. |
| `GHTMX-E0402` | A route registration cannot be resolved statically (dynamic path, unresolvable handler expression), or a package in `routeScope` cannot be fully analyzed (parse errors). | Restructure to a literal path and named handler, declare it with `//ghtmx:route VERB /path pkg.Handler`, or fix the package's parse errors. |
| `GHTMX-E0403` | A `//ghtmx:route` annotation is malformed, a `//ghtmx:routeprefix` directive is malformed, or two files in one package declare different prefixes. | Use `//ghtmx:route VERB /path pkg.Handler` (the symbol resolves through the file's imports) or `//ghtmx:routeprefix /static/prefix`, one prefix per package. |
| `GHTMX-E0404` | Two generated central-package symbols would collide (routes and/or events), or a route claims a reserved central symbol (`HTMXScript`). | Rename a handler or event; base-package prefixing is applied before this fires. |
| `GHTMX-E0501` | An `hx-*` construct requires a newer htmx than the pinned `htmxVersion`. | Raise `htmxVersion` or avoid the construct. |
| `GHTMX-E0502` | The configured `htmxVersion` has no pinned script asset. | Use a version from `ghtmx.SupportedHtmxVersions`. |
| `GHTMX-E0601` | Carve-out 1: a verb attribute carries an arbitrary expression instead of a typed binding. | Bind a handler symbol or generated constructor (see `SYNTAX.md`). |
| `GHTMX-E0602` | Carve-out 1: a verb attribute carries a string URL. | Replace the URL with a handler-symbol or constructor binding. |
| `GHTMX-E0603` | Carve-out 2: an explicit `ghtmx.URL`/`SafeURL` call at a binding site. | Remove it; binding-site escaping is engine-determined via the generated constructor. |

## Warnings

| ID | Cause | Remedy |
| --- | --- | --- |
| `GHTMX-W0101` | A fragment is never rendered or bound from any template or handler. Go-source calls to the generated `<name>Fragment(...)` entry point count as rendering (detected name-based by route discovery's syntax-only scan, so it must be a direct or qualified call within the route scope). | Reference it with `@name(...)` in a template, render `nameFragment(...)` from a handler, or delete it. |
| `GHTMX-W0102` | A declared event is never referenced by any template listener. | Listen with `hx-on:<wire>` or `hx-trigger`, or accept the warning if only handler-side emission is intended. |
| `GHTMX-W0103` | Reserved for a fragment-scope heuristic that is not yet emitted; today the fragment-scope contract is enforced solely by the Go compile error (see the D9 table below). | Add the value to the fragment's parameter list. |
| `GHTMX-W0104` | A discovered route is never bound from any template. | Bind it with an `hx-*` attribute, or accept the warning for full-page routes. A route declared with `//ghtmx:route` can carry a trailing `nav` marker — `//ghtmx:route GET /audit handlers.AuditLog nav` — exempting that route without turning the check off project-wide. |
| `GHTMX-W0201` | A constant `hx-target`/`hx-select` selector matches no static `id` in the compiled set. | Fix the selector or the id; computed selectors are exempt. Promote to error with `strictTargets`. |
| `GHTMX-W0301` | A generated file on disk does not match what the compiler would emit (hand-edited or stale). | Run `ghtmx generate`; in `-check` mode this is the drift report and exits non-zero. |

## Go compile errors with `.ghtmx` causes (D9)

Some contracts are enforced by delegating to the Go compiler: the
generated code pins symbols so violations surface as ordinary build
errors. The common shapes:

| Go compile error | `.ghtmx` cause | Remedy |
| --- | --- | --- |
| `undefined: ghtmxgen.EmitFoo` (or `FooPayload`) | Handler code emits an event no `event Foo(...)` declares — emitters exist only for declared events. | Declare the event in a template file, or fix the emitter name. |
| `cannot use ... as ... in argument to ghtmxgen.EmitFoo` | The emitted payload's fields or types disagree with the `event` declaration. | Match the declared parameter list; it is the single source of the payload type. |
| `undefined: handlers.CreateUser` reported in a `*_ghtmx.go` file | A symbol-bound handler was renamed or deleted: the generated file pins each bound symbol with a blank-identifier reference. | Rename the binding in the template, or restore the handler; then regenerate. |
| `undefined: ghtmxgen.GetUser` after a route change | A constructor-bound route was removed or renamed and the central package was regenerated. | Update the template binding; `ghtmx generate` reports `GHTMX-E0101` at the binding site first. |
| `undefined: x` inside a `ghtmxFragmentBody_*` function | A fragment body used an enclosing-scope variable: bodies compile as top-level functions and see only their declared parameters. | Add the variable to the fragment's parameter list. |
| `cannot use ... as ghtmx.SafeURL value` in a `*_ghtmx.go` file | A verb-attribute expression that is not a generated route constructor slipped past the carve-out: every binding compiles through a `ghtmx.SafeURL`-typed variable as the escaping backstop. | Bind a handler symbol or generated constructor (see `SYNTAX.md`). |
| `Foo redeclared in this block` mentioning a `*_ghtmx.go` file | A Go declaration collides with a template/fragment name, or the root `ghtmx` package was imported explicitly in a template file (generated code already imports it). | Rename one declaration; never import the root package in `.ghtmx` files. |
