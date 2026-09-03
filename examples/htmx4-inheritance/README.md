# htmx 4: explicit inheritance

htmx 4 inherits no attribute unless the ancestor says so. This example
pins `4.0.0` in its own `ghtmx.json` and shows the two name modifiers
that replace htmx 2's implicit inheritance:

- `hx-target:inherited="#out"` and `hx-include:inherited="#team"` on the
  panel reach every button beneath it;
- `hx-include:inherited:append="#role"` on the nested group extends the
  inherited value instead of replacing it.

Every button posts to the same bound route (`hx-post={ Assign }`); the
handler echoes the fields it received, so the differences between the
three responses are entirely what the markup around each button chose
to include.

```sh
ghtmx generate && go run ./cmd    # serves http://127.0.0.1:8086/inherit
```

Remove `:inherited` from the panel's `hx-target` and run
`ghtmx generate` again: the compiler reports `GHTMX-W0202`, naming the
panel, the attribute, and the button that would no longer find its
target — under htmx 2 that markup was valid, under htmx 4 it is valid
and silently wrong, which is exactly what the warning exists for. Pin
the project back to `2.0.10` and the same modifiers are reported as
introduced in 4.0.0 (`GHTMX-E0501`).
