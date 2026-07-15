# AGENTS.md — LXD Agent Instructions

LXD is a modern, secure system container and virtual machine manager written in Go.
Module: `github.com/canonical/lxd`.

## Prerequisites

LXD requires Go 1.26.5 or higher and is only tested with the Golang compiler.
- CGO native dependencies (dqlite, liblxc). Fetch them once with:

  ```bash
  make deps
  ```

  This populates `vendor/` with the native libraries. Set the environment variables
  printed by `make env` if you build outside of `make`.

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
  lint/         Lint scripts
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
