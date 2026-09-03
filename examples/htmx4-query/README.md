# htmx 4: QUERY and morph swaps

htmx 4 adds the `QUERY` method — safe and idempotent like `GET`, with
the parameters in the body like `POST` — as `hx-query`, and ghtmx binds
it like the other five verbs: `hx-query={ Search }` resolves against
the `QUERY /search` registration at build time. This example pins
`4.0.0` in its own `ghtmx.json` and pairs the new verb with the new
swap style:

- the search box issues a `QUERY` on every change (debounced) and on
  the `search` event that clears the field;
- the handler reads the body itself — `net/http` parses form bodies
  only for `POST`, `PUT`, and `PATCH`;
- the result list is swapped with `hx-swap="innerMorph"`, so entries
  with stable ids keep their DOM nodes across responses — only rows
  that actually appear run the CSS `appear` animation, where an
  `innerHTML` swap would flash every row on every keystroke;
- the page's `htmx-config` adds `open` to `morphIgnore`, so the
  `<details>` a visitor expanded stay open: a morph syncs attributes
  from the response, and the server never sends `open`.

```sh
ghtmx generate && go run ./cmd    # serves http://127.0.0.1:8088/search
```

Under a `2.0.10` pin both `hx-query` and `innerMorph` are reported as
introduced in 4.0.0 (`GHTMX-E0501`), and `ghtmx routes` lists the
`QUERY` registration beside the others.
