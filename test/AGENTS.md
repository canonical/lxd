# AGENTS.md — Test Suite

This file provides agent instructions specific to the `test/` directory.
See `../AGENTS.md` at the repository root for the full development runbook.
See `README.md` in this directory for test coding conventions and environment variables.

## Running tests

Integration tests require root. Use `sudo -E` to preserve environment variables:

```bash
# Run all tests
sudo -E ./test/main.sh group:all

# Run a specific group
sudo -E ./test/main.sh group:cluster

# Run against specific backends
sudo -E LXD_BACKENDS="btrfs lvm" ./test/main.sh group:standalone

# Repeat tests (useful to catch races)
sudo -E LXD_REPEAT_TESTS=10 ./test/main.sh group:standalone
```

Unit tests (no root needed):

```bash
make check-unit
```

## Shell script style (test/*.sh)

- No trailing whitespace, and no tab characters (use spaces for indentation).
- New `test_*` functions must be registered in `test/includes/test-groups.sh`
  (checked by `test/lint/test-tests.sh`).
- Avoid `grep -q` in pipelines under `set -o pipefail` (can cause SIGPIPE issues);
  avoid `grep -v` at the end of a pipeline to test for absence of a pattern — its
  exit code doesn't reliably reflect that.
