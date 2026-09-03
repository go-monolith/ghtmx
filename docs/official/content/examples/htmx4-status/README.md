# htmx 4: status routing and partials

htmx 4 swaps every response, and `hx-status:<code>` decides per status
where it goes — the built-in replacement for the htmx 2
`response-targets` extension. This example pins `4.0.0` in its own
`ghtmx.json` and puts the whole story on one form:

- `hx-status:422="target:#errors"` — validation problems answer 422 and
  land in the error region, not in `#result`;
- `hx-status:5xx="swap:none"` — a server error's body is never swapped
  in (htmx 4 would otherwise show it);
- `hx-disable="find button"` — htmx 4's name for what htmx 2 called
  `hx-disabled-elt`: the submit button is disabled while the request
  is in flight;
- on success the same response fills `#result` and carries two
  `<hx-partial hx-target="…" hx-swap="…">` elements that update
  `#errors` and the "last signup" footer — each names its own target
  and swap, the htmx 4 form of an out-of-band swap.

```sh
ghtmx generate && go run ./cmd    # serves http://127.0.0.1:8087/signup
```

The status suffix (`422`, `5xx`, `40x`), the config keys inside the
value (`target:`, `swap:`, `select:`), and the partials' targets are
all validated against the pinned surface; under a `2.0.10` pin the
`hx-status:` attributes are reported as introduced in 4.0.0
(`GHTMX-E0501`).
