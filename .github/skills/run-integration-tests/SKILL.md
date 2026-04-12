---
name: run-integration-tests
description: >
  Use this skill when answering questions about or performing any of the
  following: fixing a bug or adding a feature in lxd/, lxc/, or shared/;
  writing, modifying, or locating a test_*() function in test/suites/; asked to
  run or explain how to run test/vmtest.sh; debugging a failing integration
  test; registering a test in test/includes/test-groups.sh; or deciding which
  test group to run after a code change.
---

## Overview

Integration tests run inside an ephemeral LXD virtual machine via
`test/vmtest.sh`. The wrapper launches a fresh VM using the `lxd-test` profile,
builds the LXD binaries inside the VM (if not already present), runs
`test/main.sh` with the supplied arguments, streams output back to the host, and
stops the VM when done.

The test functions live in `test/suites/*.sh` and are named `test_<name>`. The
`test/includes/test-groups.sh` file groups these tests into named groups (e.g.
`group:standalone`, `group:cluster`).

## Prerequisites

The `lxd-test` LXD profile must exist before running any test. Set it up once:

```bash
GIT_ROOT="$(git rev-parse --show-toplevel)"
lxc profile list | grep -qwF lxd-test || lxc profile create lxd-test
sed "s|@@PATH_TO_LXD_GIT@@|${GIT_ROOT}|" "${GIT_ROOT}/doc/lxd-test.yaml" | lxc profile edit lxd-test
```

On machines with less than 8 CPU cores or 8 GiB of RAM, reduce the profile
limits to match available hardware:

```bash
lxc profile set lxd-test limits.cpu=2
lxc profile set lxd-test limits.memory=4GiB
lxc profile device set lxd-test root size=8GiB
```

`jq` and `uuidgen` must also be available on the host.

## Choosing what to run

Match the change to the smallest test scope that exercises it:

| Change area | Suggested target |
|---|---|
| `lxd/`, `shared/`, `client/` general | `basic_usage` |
| Networking code | `group:network` |
| Storage drivers | `group:standalone_storage` |
| Clustering | `group:cluster` |
| Container/VM device | relevant test in `group:instance` |
| Unknown / broad refactor | `group:all` (slow; prefer a narrower group first) |

To list all available groups and individual test names, look at
`test/includes/test-groups.sh`.

## Running tests

Run a single named test (maps to `test_<name>()` in `test/suites/`):

```bash
./test/vmtest.sh basic_usage
```

Run an entire group:

```bash
./test/vmtest.sh group:standalone
```

Run multiple targets in one VM:

```bash
./test/vmtest.sh basic_usage container_copy
```

Apply a timeout (uses `timeout(1)` syntax: `30s`, `10m`, `2h`):

```bash
./test/vmtest.sh --timeout=30m basic_usage
```

Keep artifacts after a successful run for later inspection:

```bash
./test/vmtest.sh --keep-artifacts basic_usage
```

If `lxc` on the host resolves to a snap wrapper that cannot reach the local
daemon, override it:

```bash
LXC_BIN=/snap/lxd/current/bin/lxc ./test/vmtest.sh basic_usage
```

## Environment variables

These are forwarded into the VM when set on the host:

| Variable | Purpose |
|---|---|
| `LXD_BACKEND` | Storage backend to use (e.g. `dir`, `btrfs`, `zfs`) |
| `LXD_BACKENDS` | Space-separated list of backends for multi-backend runs |
| `LXD_SKIP_TESTS` | Space-separated test names to skip |
| `LXD_REQUIRED_TESTS` | Space-separated tests that must pass |
| `LXD_REPEAT_TESTS` | Repeat tests N times (for flakiness checking) |
| `LXD_VERBOSE` | Set to `1`, `client`, or `server` for verbose output |
| `LXD_DEBUG` | Set to `1`, `client`, or `server` for debug output |
| `LXD_VM_TESTS` | Set to `0` to skip VM-specific tests (default: `1`) |

## Debugging failures

Use `--caffeinate` to keep the VM alive after the test exits so you can inspect
its state:

```bash
./test/vmtest.sh --caffeinate=30m basic_usage
```

The wrapper prints the reconnect command when it leaves the VM running:

```
==> Leaving lxdtest-<uuid> running for 30m
==> Reconnect with: lxc exec lxdtest-<uuid> -- bash
```

On failure, the wrapper always retains the artifacts directory. The path is
printed at startup:

```
ARTIFACTS_DIR=/tmp/vmtest.lxdtest-<uuid>.XXXXXX
```

Artifacts include:
- `guest-output.log` — full stdout/stderr from the test run
- `cloud-init.log` — cloud-init output from VM startup
- `lxc-info.txt` — `lxc info --show-log` for the instance
- `journalctl.txt` — full journal from the VM

## Writing and registering a new test

<!-- BEGIN TEST RECOMMENDATIONS -->

### `sub_test` usage

Use `sub_test` to label meaningful phases within a test and make logs easier to scan.
Prefer a small number of focused sub-tests over excessive nesting.
Use `sub_test` before a logical group of commands that verifies a specific expected behavior for a bug fix or feature.
Comments within the sub-test block are appropriate to explain why specific commands are used, any setup or initial configuration, and other intent that isn't obvious from the commands.

Good:

```sh
sub_test "Verify intended behavior X"
...
sub_test "Verify intended behavior Y"
...
```

### `echo` context

Prefer `sub_test` labels and concise comments for context instead of adding `echo` statements.
Use `echo` only when you need to debug flaky behavior.

### Expected failure

If a command is expected to fail, special care needs to be used in testing.

Bad:

```sh
set -e
...

! cmd_should_fail

some_other_command
```

Good:

```sh
set -e

! cmd_should_fail || false

some_other_command
```

Best:

```sh
set -e

if cmd_should_fail; then
  echo "ERROR: cmd_should_fail unexpectedly succeeded, aborting" >&2
  exit 1
fi

some_other_command
```

In the "bad" example, if the command unexpectedly succeeds, the script won't
abort because `bash` ignores `set -e` for compounded commands (`!
cmd_should_fail`).

The "good" example works around the problem of compound commands by falling
back to executing `false` in case of unexpected success of the command.

The "best" example also works around the problem of compound commands but in a
very intuitive and readable form, albeit longer.

````{note}
This odd behavior of `set -e` with compound commands does not apply inside `[]`.

```sh
set -e
# Does the right thing of failing if the file unexpectedly exist
[ ! -e "should/not/exist" ]
```

However, note that in the above example, if the `!` is moved outside of the `[]`, it would also warrant a ` || false` fallback.
````

For error message assertions, prefer single-quoted strings so error text with `"` does not require escaping and the comparisons stay readable.
<!-- END TEST RECOMMENDATIONS -->

### Registering a new test

1. Add a `test_<name>()` function to the appropriate file in `test/suites/`.
   Name the function to match the test group it belongs to (e.g. a clustering
   test should go in `test/suites/clustering.sh`).

2. Register the test name (without the `test_` prefix) in the correct group
   array in `test/includes/test-groups.sh`:

   ```bash
   readonly test_group_standalone=(
       ...
       "your_new_test"
   )
   ```

3. Verify the test is discoverable and runs:

   ```bash
   ./test/vmtest.sh your_new_test
   ```

## Important notes

- `vmtest.sh` requires root on the host so it can talk to the local LXD daemon.
- Each run launches a fully **ephemeral** VM; state is not shared between runs.
- The `lxd-test` profile mounts the repo at `/root/lxd` inside the VM. The
  wrapper builds LXD binaries inside the VM on first use; subsequent runs reuse
  them if they are already in `GOPATH/bin`.
- Artifacts in `/tmp` are deleted after a successful, non-caffeinated run unless
  `--keep-artifacts` is passed.

