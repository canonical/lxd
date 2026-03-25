# LXD Copilot Instructions

## Overview

LXD is a modern, secure system container and virtual machine manager written in Go.

LXD requires Go 1.26.1 or higher and is only tested with the Golang compiler.

## Project Layout and Architecture

### Directory Structure

```
/
├── lxd/                    # Main server daemon source
├── lxc/                    # Client CLI tool source  
├── lxd-agent/              # VM agent source
├── lxd-convert/            # Conversion tool source
├── client/                 # Go client library
├── shared/                 # Shared code between components
├── test/                   # Integration test suite
│   ├── suites/             # Test suites by functionality
│   ├── lint/               # Linting scripts
│   └── main.sh             # Test runner
├── doc/                    # Documentation (Sphinx)
└── scripts/                # Utility scripts
```

### Key Configuration Files

- **`.golangci.yaml`** - Go linting configuration (extensive rules)
- **`staticcheck.conf`** - Additional static analysis config
- **`.github/workflows/tests.yml`** - Main CI pipeline
- **`go.mod`** - Go module definition and dependencies
- **`Makefile`** - Primary build system
- **`doc/Makefile`** - Documentation build system

### Generated Files (Do Not Edit Manually)

Files that are auto-generated and should be updated via make targets:
- `doc/metadata.txt` - Updated via `make update-metadata`
- `lxd/metadata/configuration.json` - Updated via `make update-metadata`
- `lxd/auth/entitlements_generated.go` - Updated via `make update-auth`
- `lxd/auth/drivers/openfga_model.openfga` - Updated via `make update-auth`
- `go.mod`/`go.sum` - Updated via `make update-gomod`
- Protobuf files - Updated via `make update-protobuf`
- API schema files - Updated via `make update-schema` and `make update-api`
- Go dependencies lists - Updated via `make update-godeps`
- Formatted code - Updated via `make update-fmt`

### API and Protocol Files

- `lxd/api*.go` - REST API endpoint handlers
- `shared/api/` - API data structures
- `doc/rest-api.yaml` - OpenAPI specification
- `doc/rest-api.md` - Human-readable API docs

## Continuous Integration

### GitHub Actions Workflows

The CI pipeline (`.github/workflows/tests.yml`) runs:

1. **Code Tests** (Ubuntu 22.04):
   - Dependency review
   - ShellCheck analysis
   - Go build with minimum version check
   - Binary size validation
   - golangci-lint analysis
   - Static analysis
   - Unit tests

2. **System Tests** (matrix):
   - **Suites**: cluster, standalone
   - **Backends**: dir, btrfs, lvm, zfs, ceph, random
   - Requires root privileges
   - Uses MicroCeph and MicroOVN for storage/networking

3. **Client Tests** (cross-platform):
   - Ubuntu, macOS, Windows
   - CGO-disabled builds
   - Architecture: amd64, arm64

4. **Documentation**:
   - Sphinx build
   - Link checking  
   - Spell checking
   - Inclusive language checking

5. **UI E2E Tests**:
   - Playwright browser tests
   - Requires OIDC test credentials

### Validation Steps Before Committing

See `.github/skills/pre-commit-checks/SKILL.md`.

## Development Guidelines

### Test Recommendations

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

### Commit Requirements

- **All commits MUST be signed:** Use `git commit -s`
- **Cryptographic signatures required:** See GitHub's commit signature verification docs
- **Conventional commit structure:** Logical, reviewable changes

**Commit message structure:**

<!-- BEGIN COMMIT STRUCTURE -->
| Type                 | Affects files                                    | Commit message format               |
|----------------------|--------------------------------------------------|-------------------------------------|
| **API extensions**   | `doc/api-extensions.md`, `shared/version/api.go` | `api: Add XYZ extension`            |
| **Documentation**    | Files in `doc/`                                  | `doc: Update XYZ`                   |
| **API structure**    | Files in `shared/api/`                           | `shared/api: Add XYZ`               |
| **Go client package**| Files in `client/`                               | `client: Add XYZ`                   |
| **CLI changes**      | Files in `lxc/`                                  | `lxc/<command>: Change XYZ`         |
| **LXD daemon**       | Files in `lxd/`                                  | `lxd/<package>: Add support for XYZ`|
| **Tests**            | Files in `test/`                                 | `test/<path>: Add test for XYZ`     |
| **GitHub**           | Files in `.github/`                              | `github: Update XYZ`                |
| **Makefile**         | `Makefile`                                       | `Makefile: Update XYZ`              |

<!-- END COMMIT STRUCTURE -->

### Code Style

- Follow `golangci-lint` rules (see `.golangci.yaml`)
- Use `gofmt` for formatting
- Comment all functions and exported types
- Write table-driven unit tests where applicable
- End comments with periods and use Go doc links where possible
- Use early returns when possible to reduce nesting and improve readability
- Avoid inline variable declarations in `if` statements. Prefer assigning on its own line before the `if`.
- Use effective Go (see https://go.dev/doc/effective_go)

### Common Patterns

- **Error handling**:
  ```go
  if err != nil {
      return fmt.Errorf("Failed doing something: %w", err)
  }
  ```
- **Error message format**: Use gerund format for error messages: `Failed connecting to target` instead of `Failed to connect to target`. Another example is to better use `Failed retrieving current cluster config` instead of `Failed to retrieve current cluster config`.
- **No "unable to"**: Use `cannot` instead (e.g. `Cannot open file` not `Unable to open file`).
- **Avoid inline variable declarations in `if` statements**:
  Bad:
  ```go
  if count := len(values); count < 1 {
      return nil
  }

  if role, ok := roles[name]; ok {
      return role
  }
  ```
  Good:
  ```go
  count := len(values)
  if count < 1 {
      return nil
  }

  role, ok := roles[name]
  if ok {
      return role
  }
  ```
- **No contractions** in error messages: Use `does not` not `doesn't`, `cannot` not `can't`.
- These error message rules apply to Go string literals only, not to comments.
- When changing error messages, update any integration test assertions in `test/suites/*.sh` that match the old text.
- Always use proper English grammar in error messages, even if it results in longer messages. Avoid abbreviations or slang that may be unclear to non-native speakers.

### Shell Script Style

- **Prefer simplicity over abstraction**: Inline code is preferred over helper functions or variables unless reuse is clear and immediate.
- **Combine similar checks**: Merge related patterns into a single grep/git-grep call using alternation rather than running separate invocations.

### File Location Patterns

- **API handlers**: `lxd/_.go`
- **Device drivers**: `lxd/device/`
- **Storage drivers**: `lxd/storage/drivers/`
- **Network code**: `lxd/network/`
- **Database code**: `lxd/db/`
- **Tests**: `*_test.go` alongside source files
- **Integration tests**: `test/suites/*.sh`
