### Added

- Four more first-party render adapters — `adapters/beego` (Beego v2),
  `adapters/iris` (Iris v12), `adapters/revel` (Revel), and
  `adapters/martini` (Martini) — extending FR-035 automatic render-mode
  selection to every framework in common circulation alongside the
  existing chi, echo, gin, and fiber adapters. Each is a nested module
  released in lockstep as `adapters/<name>/vX.Y.Z` and delegates to
  `adapters/nethttp`, so mode selection, status, and header behavior
  are identical by construction. Revel's adapter is `Result`-based to
  match that framework's controller idiom; Martini is archived upstream
  and its adapter pins the framework's last published revision.
