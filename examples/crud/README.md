# The reference CRUD application

The MVP acceptance demonstration (spec task 67): a TodoMVC-style todo
manager with create, read, update (inline title edit and done-toggle),
delete, active/completed filters, toggle-all, and clear-completed —
every htmx interaction a partial update, with **zero hand-written htmx
glue**. `TestZeroHandWrittenGlue` enforces that claim mechanically.
The filter buttons reuse the bound `/todos` route with `hx-vals`; the
bulk operations are their own bound routes announcing themselves
through one merged `todos-bulk-changed` event. The store is capped
(100 todos, 200-character titles) because the app also runs as a
public live demo on the documentation site.

```bash
go run ./cmd      # serves http://localhost:8080
go test .         # end-to-end suite incl. the break-the-build proofs
```

## Where each requirement lives

| Requirement | Demonstration |
| --- | --- |
| FR-020/021 — route-aware bindings | `crud.ghtmx`: `hx-post={ CreateTodo }` / `hx-get={ ListTodos }` / `hx-get={ TodoStats }` (symbol bindings, paths folded statically) and `ghtmxgen.EditTodo/ToggleTodo/RenameTodo/DeleteTodo(t.ID)` (typed constructors with escaped parameters) |
| FR-031 — compile-time fragments | `todoList` (page-embedded and standalone byte-identically — asserted by `TestListModeSelection`), nested `todoRow`, handler-only `todoEditRow`, `statsPanel` |
| FR-035 — adapter mode selection | `ListTodos` via `nethttp.WithPage`: browsers get the page, htmx gets the bare list |
| FR-037 — event contract | `event TodoCreated/TodoToggled/TodoDeleted/TodosBulkChanged` declarations; handlers emit only `ghtmxgen.Emit*`; the stats panel listens with `hx-trigger="todo-created from:body, …"` |

## The break-the-build guarantee

`TestRouteChangesBreakTheBuild` copies the app into a scratch module and
proves the acceptance criterion three ways: deleting a bound route fails
generation with `GHTMX-E0101` naming the binding; renaming a handler
(registration included) fails generation the same way; and renaming a
symbol-bound handler after generation breaks `go build` immediately
through the generated blank-identifier pin. Constructor bindings break
at the next `ghtmx generate`, which watch mode and CI keep in the loop.

## Known-informational diagnostics

Generation reports two warnings by design: `GHTMX-W0101` for
`todoEditRow` (rendered only from a handler — the reachability check
sees template references, not Go bodies) and `GHTMX-W0104` for the
`GET /` page route (a full-page route is navigated to, not bound).
