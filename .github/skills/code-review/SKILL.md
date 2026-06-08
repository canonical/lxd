---
name: code-review
description: >
  Use this skill when reviewing a pull request or diff for LXD. Triggers on
  requests to review code, check a PR, or evaluate changes before merging.
---

## Purpose

Apply LXD-specific checks that go beyond what `golangci-lint`, `gofmt`, and the
project's lint scripts handle automatically. Focus only on issues that would
require a human to act — skip anything static analysis already enforces.

## Checklist

### Go code style

- **Prefer short variable declarations.** Flag two-statement declare-then-assign when `:=` would work.

  ```go
  // Bad — not caught by golangci-lint
  var err error
  err = fn()

  // Good
  err := fn()
  ```

- **Use `shared/` helpers** before re-implementing utilities. Common ones:
  - `shared.ParseCert([]byte)` — PEM certificate parsing
  - `shared.CertFingerprint(cert)` — SHA-256 fingerprint
  - `shared.SplitNTrimSpace(s, sep, n, trim)` — split + trim

### Shell / integration tests

- **`jq` must use `--exit-status` / `-e`** when asserting field presence or values.
  A bare `jq '.field' file` that discards output is a silent false-negative.

- **Expected failure pattern:** Flag `! cmd` without `|| false` when `set -e` is active.
  The correct patterns are `! cmd || false` or an explicit `if cmd; then … exit 1; fi`.

- **No `grep -c` for presence/absence checks.** Use `grep -wF` for presence or exact
  output comparison (e.g., `lxc list -f csv -c n`). `grep -c` for actual counts is fine.

- **`sub_test` labels** should be present before each logically distinct phase of a test.
  Flag tests that have many sequential commands with no `sub_test` labeling.

### Commit requirements

See [`COMMITS.md`](../../../COMMITS.md) for the full prefix table and signing requirements.
Flag any commit missing `Signed-off-by:` or a cryptographic signature.

### API and documentation changes

- If `shared/api/` is modified, check that `doc/rest-api.yaml` and `doc/api-extensions.md`
  are updated as needed.
- New or changed configuration keys require `make update-metadata` to regenerate
  `lxd/metadata/configuration.json` and `doc/metadata.txt`.

### Auto-generated files

Flag any direct edits to files that must be updated via `make` targets:

- `lxd/auth/entitlements_generated.go` → `make update-auth`
- `lxd/auth/drivers/openfga_model.openfga` → `make update-auth`
- `doc/rest-api.yaml` → `make update-api`
- `lxd/metadata/configuration.json`, `doc/metadata.txt` → `make update-metadata`
