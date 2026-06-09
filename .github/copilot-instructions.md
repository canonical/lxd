# LXD Copilot Instructions

LXD is a modern, secure system container and virtual machine manager.
LXD requires Go 1.26.4 or higher and is only tested with the Golang compiler.
See `AGENTS.md` for the full development runbook.

## Repository layout

```
lxd/        Main daemon          lxc/        Client CLI
client/     Go client library    shared/     Shared utilities
lxd-agent/  VM guest agent       test/       Integration tests
doc/        Sphinx docs          scripts/    Utility scripts
```

Key locations: API handlers `lxd/api*.go`, device drivers `lxd/device/`, storage `lxd/storage/drivers/`, network `lxd/network/`, database `lxd/db/`, unit tests `*_test.go`, integration tests `test/suites/*.sh`.

## Commit requirements

See [`COMMITS.md`](../COMMITS.md) for the full commit prefix table and signing requirements.

## Code style

- **No inline variable declarations in `if`** — assign on a separate line before the `if`.
- **Check `shared/` for helpers** before reimplementing: `shared.ParseCert`, `shared.CertFingerprint`, `shared.GetRemoteCertificate`, `shared.SplitNTrimSpace`.
- Comment all exported functions and types; end comments with a period.
- Write table-driven unit tests where applicable.

## Error messages (Go string literals only)

- Gerund form: `"Failed connecting"` not `"Failed to connect"`.
- Use `"cannot"` not `"unable to"`; no contractions (`"does not"` not `"doesn't"`).
- US English spelling: `behavior`, `canceled`, `initialize`.
- When changing error messages, update matching assertions in `test/suites/*.sh`.
