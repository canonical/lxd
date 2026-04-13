#!/usr/bin/env bash
# Sets up a synthetic git repo for eval 4: multi-commit PR with a mix of
# essential and cosmetic commits, some of which conflict on the target branch.
# Designed to be fast to execute (small files) while exercising the same
# decision-making as a real-world backport.
# Usage: bash setup_mixed_backport.sh [target-dir]
# Prints:
#   REPO=<path>
#   BRANCH=<target branch>
#   COMMITS=<space-separated SHAs in topological order>

set -euo pipefail

REPO="${1:-/tmp/backport-eval-mixed}"
rm -rf "$REPO"
mkdir -p "$REPO"
cd "$REPO"

git init -q
git config user.email "test@example.com"
git config user.name "Test"
git config rerere.enabled false

# ---- shared base on main ----
cat > server.go <<'EOF'
package main

import "fmt"

func handleRequest(cfg map[string]string) error {
addr := cfg["core.https_address"]
if addr == "" {
return fmt.Errorf("node config missing address")
}
return listen(addr)
}

func listen(addr string) error {
fmt.Printf("listening on %s\n", addr)
return nil
}
EOF

cat > errors.go <<'EOF'
package main

const errNodeConfig = "node config error"
EOF

git add server.go errors.go
git commit -q -m "initial commit"

# ---- stable-5.0 diverges: different server.go structure ----
git checkout -q -b stable-5.0

cat > server.go <<'EOF'
package main

import "fmt"

func handleRequest(cfg map[string]string) error {
addr := cfg["core.https_address"]
if addr == "" {
return fmt.Errorf("node config missing address")
}
// stable-5.0 uses a legacy listener
return legacyListen(addr)
}

func legacyListen(addr string) error {
fmt.Printf("[legacy] listening on %s\n", addr)
return nil
}
EOF
git add server.go
git commit -q -m "stable-5.0: use legacyListen"

# ---- back to main: add the feature branch commits ----
git checkout -q main 2>/dev/null || git checkout -q -b main

# Commit A (cosmetic): rename error string (will conflict on stable because
# errors.go on stable still has the old string referenced elsewhere)
cat > errors.go <<'EOF'
package main

const errNodeConfig = "member config error"
EOF
git add errors.go
git commit -q -m "rename: node config -> member config in error strings"
SHA_A=$(git rev-parse HEAD)

# Commit B (essential): fix the bug — don't overwrite cfg on revert
cat > server.go <<'EOF'
package main

import "fmt"

func handleRequest(cfg map[string]string) error {
addr := cfg["core.https_address"]
if addr == "" {
return fmt.Errorf("node config missing address")
}
oldAddr := addr
if err := listen(addr); err != nil {
// BUG FIX: restore original address instead of wiping cfg
cfg["core.https_address"] = oldAddr
return err
}
return nil
}

func listen(addr string) error {
fmt.Printf("listening on %s\n", addr)
return nil
}
EOF
git add server.go
git commit -q -m "fix: restore original address on revert instead of wiping cfg"
SHA_B=$(git rev-parse HEAD)

# Commit C (cosmetic): fix typo in comment (applies cleanly, no conflict)
cat > server.go <<'EOF'
package main

import "fmt"

func handleRequest(cfg map[string]string) error {
addr := cfg["core.https_address"]
if addr == "" {
return fmt.Errorf("node config missing address")
}
oldAddr := addr
if err := listen(addr); err != nil {
// BUG FIX: restore original address instead of wiping config
cfg["core.https_address"] = oldAddr
return err
}
return nil
}

func listen(addr string) error {
fmt.Printf("listening on %s\n", addr)
return nil
}
EOF
git add server.go
git commit -q -m "comment: fix typo cfg -> config"
SHA_C=$(git rev-parse HEAD)

# Commit D (essential): add reinstate logic for cluster address (new function, no conflict)
cat >> server.go <<'EOF'

func reinstateClusterAddr(cfg map[string]string) {
if cfg["cluster.https_address"] != "" && cfg["core.https_address"] == "" {
// Reinstate cluster address when core address is unset
_ = listen(cfg["cluster.https_address"])
}
}
EOF
git add server.go
git commit -q -m "feat: reinstate cluster.https_address listener when core.https_address is unset"
SHA_D=$(git rev-parse HEAD)

git checkout -q stable-5.0

echo "REPO=$REPO"
echo "BRANCH=stable-5.0"
echo "COMMITS=$SHA_A $SHA_B $SHA_C $SHA_D"
echo ""
echo "Commit summary:"
echo "  SHA_A=$SHA_A  (cosmetic rename — will conflict)"
echo "  SHA_B=$SHA_B  (essential bug fix — will conflict due to legacyListen)"
echo "  SHA_C=$SHA_C  (cosmetic comment fix — will apply cleanly after B)"
echo "  SHA_D=$SHA_D  (essential new feature — applies cleanly)"
