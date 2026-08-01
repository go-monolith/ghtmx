# hx-bindings

Route-aware `hx-*` bindings end to end: a symbol binding
(`hx-get={ handlers.ListItems }`) folds a registered path into static
markup, and a typed constructor (`hx-get={ ghtmxgen.GetItem(id) }`)
substitutes URL-escaped parameters. Renaming either route breaks the
build at the binding site.

```sh
ghtmx generate && go run ./cmd    # serves http://127.0.0.1:8081/items
```

One deliberate wrinkle: the handlers live in a subpackage so the
templates can demonstrate a *cross-package* symbol binding. Because
the templates bind `handlers.*` symbols, the handlers package cannot
import the template package back — the example installs the render
bodies through the `handlers.ListItemsBody`/`GetItemBody` hooks in its
`init`. A single-package application needs none of this plumbing; the
crud example is the canonical single-package shape.
