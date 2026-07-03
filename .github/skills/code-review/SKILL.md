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

## Beyond spec compliance

Confirming a PR does what its description says is necessary but not sufficient. Also watch for
the following, which are common in this codebase and easy to miss when reading a diff in
isolation. These are in addition to the checklist above, not a replacement for it — keep running
through the Go style, shell/test, and API/doc checks too.

### Too many transactions/queries

A function can look clean and modular while hiding a problem: it opens a transaction and calls
into something that issues DB queries. Check whether that call happens inside a loop, and if so,
whether it's reloading the same data repeatedly. This matters a lot under clustering — every
query goes over the network to the leader, and dqlite holds a single cluster-wide write lock for
the duration of a write transaction. A loop of writes inside one transaction holds that lock
longer, queuing every other write transaction in the cluster behind it until commit.

### Code duplication and cyclomatic complexity

These are the two biggest structural problems in the LXD codebase. A diff can look perfectly
reasonable in isolation while being a bad addition in context. Before commenting on a changed
function, actually read the whole function in the file (not just the changed lines shown in the
diff) — don't guess or hedge about whether it's "probably" long and winding, check. If it's already
long and winding and this change adds more branches and lines, push back and ask for a refactor —
if it's not split out now, it never will be as a separate task.

If the same pattern shows up multiple times within a single PR (or across PRs from different
authors), call it out and ask for it to be factored into a shared function.

### Tests aren't free

Weigh the complexity and run time of new tests against the value they add. Don't wave through a
slow or convoluted test just because it exercises the change.

### Be cluster-aware

For every PR, ask: "does this work in a cluster?" Concretely, check for:

- **Local vs. cluster-wide state.** Does the change read or write something that's only valid on
  the local member (e.g. local filesystem paths, in-memory caches, local config) without accounting
  for the fact that the request could be handled by any member?
- **Notification/forwarding.** If an operation must apply to every member (e.g. creating a network,
  reloading a daemon setting), does it use the existing internal client / notification mechanism to
  propagate to other members, rather than assuming it only needs to run once?
- **Member targeting.** For member-specific resources (storage pools, networks, instances pinned to
  a node), does the code respect `?target=` handling and reject or route correctly when a request
  lands on the wrong member?
- **Partial failure across members.** If an operation touches multiple members (or the DB plus a
  remote member), what happens if it succeeds on some and fails on others? Is there rollback, or at
  least a clear error that leaves the cluster in a consistent, recoverable state?
- **Leader-only assumptions.** Does the code assume it's running on the cluster leader (e.g. for
  dqlite writes or heartbeats) when it could run on any member?

### What can go wrong?

Especially around network requests, ask "what can go wrong here, and is it handled?" Check for
each of the following failure modes and whether the code handles it:

- **Timeouts.** Does the request use a bounded context/timeout, or can it hang indefinitely and
  block a caller (or a cluster-wide lock) forever?
- **Partial/unreachable members.** If a member is down or unreachable mid-operation, is that
  surfaced as a clear error rather than a silent skip or a panic?
- **Network partitions.** Could a split-brain or partition cause two members to disagree about
  state (e.g. both thinking they're leader, or an operation applied twice)?
- **Retries and idempotency.** If a request is retried after a timeout, is the operation safe to
  repeat, or could it double-apply (e.g. double-create, double-charge a quota)?
- **TLS/auth failures.** Are certificate or auth errors on member-to-member calls handled and
  reported clearly, rather than surfacing as a generic connection error?
- **Untrusted/attacker-controlled input over the network.** Is data from a remote member or client
  validated before use, rather than trusted implicitly because it came over the internal API?

Keep the [fallacies of distributed computing](https://en.wikipedia.org/wiki/Fallacies_of_distributed_computing)
in mind (network is reliable, latency is zero, bandwidth is infinite, the network is secure,
topology doesn't change, there is one administrator, transport cost is zero, the network is
homogeneous).

### API consistency

Ask "has this sort of thing been done before?" and check how it was presented previously — new
endpoints and fields should follow existing conventions rather than inventing new ones.

- A DB entity with a DB ID gets its own URL in the API.
- Avoid recursive creation/update of multiple entities through a single API call. Special cases
  may exist, but it should not be the norm — flag it if a PR introduces this pattern without
  strong justification.
