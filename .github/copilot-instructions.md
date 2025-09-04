# LXD Copilot Instructions

## Overview

LXD is a modern, secure system container and virtual machine manager written in Go.

## Project Layout and Architecture

### Directory Structure

```
/
├── lxd/                    # Main server daemon source
├── lxc/                    # Client CLI tool source  
├── lxd-agent/              # VM agent source
├── lxd-migrate/            # Migration tool source
├── client/                 # Go client library
├── shared/                 # Shared code between components
├── test/                   # Integration test suite
│   ├── suites/             # Test suites by functionality
│   ├── lint/               # Linting scripts
│   └── main.sh             # Test runner
├── doc/                    # Documentation (Sphinx)
├── po/                     # Internationalization files
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
- `po/*.pot` - Updated via `make update-po` or `make update-pot`
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

1. **Run static analysis:**
   ```bash
   make static-analysis
   ```

2. **Run unit tests:**
   ```bash
   make check-unit
   ```

3. **Test build:**
   ```bash
   make
   ```

4. **Check documentation (if changed):**
   ```bash
   cd doc && make html
   ```

## Development Guidelines

### Commit Requirements

- **All commits MUST be signed:** Use `git commit -s`
- **Cryptographic signatures required:** See GitHub's commit signature verification docs
- **Conventional commit structure:** Logical, reviewable changes

**Commit message structure:**

| Type                 | Affects files                                    | Commit message format               |
|----------------------|--------------------------------------------------|-------------------------------------|
| **API extensions**   | `doc/api-extensions.md`, `shared/version/api.go` | `api: Add XYZ extension`            |
| **Documentation**    | Files in `doc/`                                  | `doc: Update XYZ`                   |
| **API structure**    | Files in `shared/api/`                           | `shared/api: Add XYZ`               |
| **Go client package**| Files in `client/`                               | `client: Add XYZ`                   |
| **CLI changes**      | Files in `lxc/`                                  | `lxc/<command>: Change XYZ`         |
| **LXD daemon**       | Files in `lxd/`                                  | `lxd/<package>: Add support for XYZ`|
| **Tests**            | Files in `tests/`                                | `tests: Add test for XYZ`           |

### Code Style

- Follow `golangci-lint` rules (see `.golangci.yaml`)
- Use `gofmt` for formatting
- Comment all functions and exported types
- Write table-driven unit tests where applicable
- End comments with periods and use Go doc links where possible
- Use early returns when possible to reduce nesting and improve readability
- Use effective Go (see https://go.dev/doc/effective_go)

### Common Patterns

**Error handling:**
```go
if err != nil {
    return fmt.Errorf("Failed to do something: %w", err)
}
```

### File Location Patterns

- **API handlers**: `lxd/_.go`
- **Device drivers**: `lxd/device/`
- **Storage drivers**: `lxd/storage/drivers/`
- **Network code**: `lxd/network/`
- **Database code**: `lxd/db/`
- **Tests**: `*_test.go` alongside source files
- **Integration tests**: `test/suites/*.sh`
