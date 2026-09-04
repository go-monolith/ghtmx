### Added

- `auth.WithSafeMethods` replaces a CSRF middleware's safe-method list,
  and `auth.DefaultSafeMethods` returns the default one (GET, HEAD,
  OPTIONS, QUERY). It is the way to decline the QUERY exemption v0.2.0
  introduced, which applies to every consumer whatever htmx version the
  project pins — the middleware is runtime code and cannot see the
  compile-time `htmxVersion`. The option works identically for
  `auth.CSRF` and for the gin, echo, fiber, and fiber v3 glue, and the
  decision now runs through `auth.CSRFOptions.SafeMethod`, so the
  safe-list cannot diverge between adapters. `AUTH.md` documents the
  change of surface and the opt-out (#45).
- `MIGRATION.md`: what breaks between ghtmx versions, the htmx 2 → 4
  path, and where each version's migration notes live. `README.md`
  gained a *Supported htmx versions* section — the support table, the
  fact that a project without a `ghtmx.json` is on the `2.0.10` default,
  and a link to the migration guide (#47).

### Fixed

- `.version` on `main` is a `X.Y.Z-dev` marker rather than a stale
  release number, so a binary built from a source checkout no longer
  reports `v0.1.0` or stamps it into every file it generates. Released
  binaries are unaffected: the release automation writes the real
  version into the tagged tree and verifies it against the tag (#48).
