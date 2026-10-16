# AGENTS.md — LXD Agent Instructions

LXD is a modern, secure system container and virtual machine manager written in Go.
Module: `github.com/canonical/lxd`.

## Prerequisites

LXD requires the Go version listed in `doc/requirements.md` or higher and is only tested with the Golang compiler.
- CGO native dependencies (dqlite, liblxc). Fetch them once with:

  ```bash
  make deps
  ```

  This builds the native libraries under `$(go env GOPATH)/deps` (or `vendor/` in a
  release tarball). Set the environment variables printed by `make env` if you build
  outside of `make`.

- The `client` (lxc CLI) and some test binaries build without CGO and have no native deps.

## Repository layout

```
lxd/            Main daemon
lxc/            Client CLI
lxd-agent/      VM guest agent
client/         Go client library
shared/         Code shared across components
test/
  suites/       Integration test suites (bash)
  snap/         Snap integration test scripts (run directly)
  local-snap.sh Snap integration test runner (sourced by test/snap/* scripts)
  lint/         Lint scripts
  includes/
    snap-helpers.sh  Shared snap integration test helpers
doc/            Sphinx documentation
```

### Auto-generated files — do not edit manually

Update these via the listed `make` target instead of editing by hand:

| File | Command |
|------|---------|
| `lxd/metadata/configuration.json`, `doc/metadata.txt` | `make update-metadata` |
| `lxd/auth/entitlements_generated.go`, `lxd/auth/drivers/openfga_model.openfga` | `make update-auth` |
| `go.mod`, `go.sum`, `tools/go.mod`, `tools/go.sum` | `make update-gomod` |
| `doc/rest-api.yaml` | `make update-api` |

## Build

```bash
# CGO-free client only (no native deps needed)
make client

# Full daemon (requires CGO deps)
make lxd

# Everything
make
```

## Validate before committing

Run these in order. Each must pass before moving to the next.

```bash
# 1. Static analysis (golangci-lint, errortype, zerolint, generated-file checks)
make static-analysis

# 2. Unit tests
make check-unit

# 3. Full build
make
```

`make static-analysis` may regenerate output (e.g. `doc/rest-api.yaml`, generated DB code,
auth entitlements, metadata docs) and errors out if that leaves an uncommitted diff; it
never prompts or commits on its own (stdin is closed for the checks it runs), so it's safe
to run from an agent session. If it reports drift, re-run `make update-api`,
`make update-auth`, `make update-metadata`, or `make update-schema` (whichever produced the
file in question) to regenerate it, then stop and leave the diff for a human to review and
commit; these four targets never call git themselves. Do not run an individual `check-*`
target (e.g. `make check-api`) or `scripts/check-and-commit.sh` directly, and do not run
other `update-*` targets like `update-gomod`, `update-golangci`, or `update-godeps` as a
substitute; unlike the four above, those call `check-and-commit.sh` directly and will
prompt interactively to commit, which hangs an agent session with no one to answer.

`make static-analysis` requires network access (it installs/updates golangci-lint,
errortype, zerolint, goimports, and go-swagger at versions pinned in `tools/go.mod`).
If you don't have network access, skip it and rely on the style rules below plus
`make check-unit` and `make` — a maintainer or CI will run the full
`make static-analysis` before merge.

## Integration tests

Integration tests require root, a running LXD daemon, and (for some suites) MicroCeph /
MicroOVN. See `test/README.md` for full setup instructions.

```bash
# Run a specific suite
sudo ./test/main.sh <suite-name>

# Run a snap integration suite
sudo -E ./test/snap/cgroup latest/edge
```

## Key conventions

### Commit format

See [`COMMITS.md`](COMMITS.md) for the full commit prefix table and signing requirements.

### Error messages

- Use gerund form: `"Failed connecting to target"` not `"Failed to connect to target"`.
- Use `"cannot"` not `"unable to"` and not contractions (`"does not"` not `"doesn't"`).
- US English spelling throughout (`behavior`, `color`, `initialize`).

### Go code style

- No inline variable declarations inside `if` conditions — assign on a separate line first.
- Prefer early returns to reduce nesting.
- Check `shared/` for existing helpers before implementing utilities from scratch.
  Common helpers include `shared.ParseCert([]byte)` for PEM parsing,
  `shared.CertFingerprint(cert)` for SHA-256 fingerprints, and
  `shared.SplitNTrimSpace(s, sep, n, trim)` for splitting and trimming.

### Additional Go style rules (enforced by `make static-analysis`)

- Use `shared.IsFalse(x)` / `shared.IsTrue(x)` directly — do not write
  `!shared.IsTrue(x)` or `!shared.IsFalse(x)`.
- Always use the parenthesized `import (...)` block, even for a single import
  (except `import "C"` for cgo).
- Do not alias `github.com/canonical/lxd/client` as `lxd` — this collides
  conceptually with the LXD daemon package name.
- Prefer structured logging: `logger.Error("message", logger.Ctx{"key": val})` over
  `logger.Errorf("message: %v", val)` — use the `f`-suffixed variants (`Debugf`,
  `Infof`, `Warnf`, `Errorf`) only when the format string actually contains a `%` verb.
- Add a blank line after a closing `}` of a block before the next statement, unless
  the next line continues the same construct (e.g. `} else {` or another `case`).

### Shell test style

- Use `jq --exit-status` (`jq -e`) when asserting field presence or values.
- For expected command failure: `if cmd_should_fail; then echo "ERROR: ..."; exit 1; fi`
- Avoid `grep -c` for presence/absence checks; use `grep -wF` or exact CSV output instead.
- Use `sub_test "..."` labels to mark meaningful test phases.

## Code review

Use these checks alongside `make static-analysis`, `make check-unit`, and
`make`; `COMMITS.md` remains the source for commit prefixes and signing rules.
Report only findings that require human action. Do not repeat issues already
enforced by static analysis or lint checks; run the relevant check and report
its output instead.

### Go and test quality

- Capitalize the first word of error messages. Preserve the gerund form,
  `cannot`, and US English conventions listed above.
- Prefer short variable declarations when a separate declaration and assignment
  are unnecessary.
- Document exported functions and types, and end doc comments with a period.
- Prefer table-driven unit tests for homogeneous cases. Consider whether new
  tests add enough value to justify their complexity and runtime.
- Read each changed function in full before commenting. Flag added branching in
  already long functions and repeated logic that should be factored out.

### Cluster and network correctness

- Check whether state is local or cluster-wide, and whether operations need
  member targeting or notifications to other members.
- Consider partial failure across members, leader-only assumptions, partitions,
  retries and idempotency, timeouts, TLS or authentication errors, and
  validation of untrusted network input.
- Avoid repeated database queries inside loops or transactions. A write
  transaction holds dqlite's cluster-wide write lock and can block other
  writes.

### API and generated files

- Follow existing API conventions. A database entity with its own ID should
  have its own URL; avoid APIs that recursively create or update multiple
  entities without a strong justification.
- Changes to `shared/api/` may also require updates to `doc/rest-api.yaml` and
  `doc/api-extensions.md`. New configuration keys require `make update-metadata`.
- Never edit generated files directly. Use the `make` targets in the generated
  files table above.

### Choosing integration tests

| Changed area | Suggested test |
|---|---|
| `lxd/cluster/` | `group:cluster` or a `clustering_*` test |
| `lxd/network/` | `group:network` |
| `lxd/storage/` | `group:standalone_storage` or `group:cluster_storage` |
| `lxd/storage/replicator/` | `group:cluster_storage` or `clustering_replicator_*` |
| `lxd/instance/` | `group:instance` |
| `lxd/auth/`, `lxd/identity/` | `auth` |
| `lxc/` | `basic` or the matching subcommand test |
| `shared/` | Search the suites for the affected consumer |

Integration tests require a live environment; report them as follow-ups when
they cannot be run. Do not clean up state after a failed test: preserve it for
inspection and debugging.
