# LXD Copilot Instructions

## Repository Overview

LXD is a modern, secure system container and virtual machine manager written in Go. It provides a unified experience for running and managing full Linux systems inside containers or virtual machines through a powerful REST API. The project scales from single instances to full data center clusters.

**Key Details:**
- **Language**: Go 1.24.5+ (required minimum version defined in GOMIN in Makefile)
- **License**: AGPL-3.0-only (some components Apache-2.0)
- **Size**: Large codebase (~150k+ lines) with extensive test suite
- **Architecture**: Client-server model with REST API
- **Runtime**: Linux only for daemon, cross-platform for client tools

## Build System and Dependencies

### Core Dependencies (Always Required)

LXD has complex C dependencies that **must** be built before Go compilation:

1. **dqlite** - Distributed SQLite database library
2. **liblxc** - Low-level container runtime library

**Critical Build Sequence:**
```bash
# 1. ALWAYS run this first - builds C dependencies
make deps

# 2. Set environment variables (displayed at end of make deps)
export CGO_CFLAGS="-I$(go env GOPATH)/deps/dqlite/include/ -I$(go env GOPATH)/deps/liblxc/include/"
export CGO_LDFLAGS="-L$(go env GOPATH)/deps/dqlite/.libs/ -L$(go env GOPATH)/deps/liblxc/lib/$(uname -m)-linux-gnu/"
export LD_LIBRARY_PATH="$(go env GOPATH)/deps/dqlite/.libs/:$(go env GOPATH)/deps/liblxc/lib/$(uname -m)-linux-gnu/"
export PKG_CONFIG_PATH="$(go env GOPATH)/deps/liblxc/lib/$(uname -m)-linux-gnu/pkgconfig"
export CGO_LDFLAGS_ALLOW="(-Wl,-wrap,pthread_create)|(-Wl,-z,now)"

# 3. Then build LXD
make
```

### Build Dependencies Installation (Ubuntu/Debian)

**Essential build dependencies:**
```bash
sudo apt install \
    autoconf \
    automake \
    build-essential \
    gettext \
    git \
    libacl1-dev \
    libapparmor-dev \
    libcap-dev \
    liblz4-dev \
    libseccomp-dev \
    libsqlite3-dev \
    libtool \
    libudev-dev \
    libuv1-dev \
    make \
    meson \
    ninja-build \
    pkg-config \
    python3-venv
```

**Runtime dependencies:**
```bash
sudo apt install \
    attr \
    iproute2 \
    nftables \
    rsync \
    squashfs-tools \
    tar \
    xz-utils
```

**Test suite dependencies:**
```bash
sudo apt install \
    acl \
    bind9-dnsutils \
    btrfs-progs \
    busybox-static \
    curl \
    dnsmasq-base \
    dosfstools \
    e2fsprogs \
    iputils-ping \
    jq \
    netcat-openbsd \
    openvswitch-switch \
    s3cmd \
    shellcheck \
    socat \
    sqlite3 \
    swtpm \
    xfsprogs \
    yq
```

### Build Targets

- `make deps` - Build C dependencies (dqlite, liblxc) - **REQUIRED FIRST**
- `make` or `make all` - Build all binaries (lxd, lxc, lxd-agent, lxd-migrate)
- `make client` - Build only the client (lxc)
- `make lxd` - Build only the server daemon
- `make lxd-agent` - Build the VM agent
- `make static-analysis` - Run linting and static analysis
- `make check-unit` - Run unit tests only
- `make check` - Run unit + integration tests (requires root privileges)

### Common Build Issues and Solutions

**Missing dqlite error:**
```
Missing dqlite, run "make deps" to setup.
```
**Solution:** Always run `make deps` before `make`.

**CGO linking errors:**
**Solution:** Ensure all environment variables from `make deps` are set.

**Permission errors during tests:**
**Solution:** Tests require root: `sudo -E make check`

**libuv1-dev missing:**
```
checking for libuv... configure: error: libuv not found
```
**Solution:** Install libuv1-dev package.

**Timeouts:** The `make deps` step can take 5-10 minutes. The full `make check` can take 30+ minutes.

## Testing and Validation

### Linting and Static Analysis

**Primary linting command:**
```bash
make static-analysis
```

This runs:
- `golangci-lint` with extensive Go rules (see `.golangci.yaml`)
- `shellcheck` on all shell scripts 
- `yamllint` on workflow files
- Custom lint scripts in `test/lint/`
- License checking with `go-licenses`

**Individual lint checks:**
```bash
# Run all lint scripts individually
run-parts --verbose --exit-on-error --regex '.sh' test/lint

# Or specific checks
test/lint/trailing-space.sh
test/lint/godeps.sh
test/lint/golangci.sh
```

### Testing

**Client tests only (fast, no dependencies):**
```bash
CGO_ENABLED=0 go test -v ./client/...
CGO_ENABLED=0 go test -v ./lxc/...
CGO_ENABLED=0 go test -v ./shared/...
```

**Unit tests only (requires build dependencies):**
```bash
make check-unit
```

**Full test suite (slow, requires root):**
```bash
sudo -E make check
```

**Integration tests only:**
```bash
cd test && sudo -E ./main.sh
```

**Test environment variables:**
- `LXD_VERBOSE=1` - Enable verbose test output
- `LXD_BACKEND=dir|btrfs|lvm|zfs|ceph` - Choose storage backend
- `LXD_SKIP_TESTS="test1 test2"` - Skip specific tests
- `LXD_REQUIRED_TESTS="test1 test2"` - Only run specific tests

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
│   ├── suites/            # Test suites by functionality
│   ├── lint/              # Linting scripts
│   └── main.sh            # Test runner
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
- `go.mod/go.sum` - Updated via `make update-gomod`
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

3. **Test client build:**
   ```bash
   CGO_ENABLED=0 go build -o /tmp/lxc-test ./lxc
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

### Code Style

- Follow `golangci-lint` rules (see `.golangci.yaml`)
- Use `gofmt` for formatting
- Comment exported functions and types
- Write table-driven tests where applicable

### Common Patterns

**Error handling:**
```go
if err != nil {
    return fmt.Errorf("Failed to do something: %w", err)
}
```

**Testing expected failures:**
```bash
# GOOD - explicit failure handling
if cmd_should_fail; then
  echo "ERROR: cmd_should_fail unexpectedly succeeded, aborting" >&2
  exit 1
fi

# BAD - bash ignores set -e for compound commands
! cmd_should_fail || false
```

### File Location Patterns

- **API handlers**: `lxd/api_*.go`
- **Device drivers**: `lxd/device/`
- **Storage drivers**: `lxd/storage/drivers/`
- **Network code**: `lxd/network/`
- **Database code**: `lxd/db/`
- **Tests**: `*_test.go` alongside source files
- **Integration tests**: `test/suites/*.sh`

## Documentation

### Building Documentation

```bash
cd doc
make html        # Build HTML docs
make serve       # Serve locally on :8000
make linkcheck   # Check external links
make spelling    # Spell check
```

**Documentation stack:**
- **Sphinx** with custom configuration
- **MyST** Markdown parser
- **Custom themes** in `doc/.sphinx/themes/`
- **Requirements**: Auto-generated `doc/.sphinx/requirements.txt`

### Documentation Files

- `doc/installing.md` - Installation instructions
- `doc/getting_started.md` - User getting started guide
- `doc/contributing.md` - Developer guide
- `CONTRIBUTING.md` - Project contribution guidelines
- `README.md` - Project overview

## Critical Notes for Agents

1. **NEVER run `make` without first running `make deps`** - it will fail
2. **Set all environment variables** shown by `make deps` before building
3. **Use `sudo -E` for tests** - they require root and environment preservation
4. **Client can be built standalone** with `CGO_ENABLED=0 go build ./lxc`
5. **Run `make static-analysis`** before proposing changes
6. **Integration tests are slow** - 30+ minutes for full suite
7. **Generated files exist** - update via make targets, not manually
8. **Architecture matters** - builds are architecture-specific for C deps
9. **Storage backend tests vary** - some require special setup (ceph, zfs)
10. **Cross-platform client** - but daemon is Linux-only

## Emergency Debugging

If builds fail mysteriously:
1. Check `make deps` completed successfully
2. Verify all environment variables are set (run `make env`)
3. Check disk space (builds need 2GB+ RAM)
4. Verify Go version >= 1.24.5
5. Check for missing build dependencies listed above

Trust these instructions - they are tested and validated. Only search for additional information if specific errors occur that aren't covered here.
