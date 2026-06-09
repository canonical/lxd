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

