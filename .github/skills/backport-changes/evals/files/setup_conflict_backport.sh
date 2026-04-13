#!/usr/bin/env bash
# Sets up a minimal git repo for eval 3: backport that results in a conflict.
# The same file is modified in both main and stable-5.0, so cherry-pick will conflict.
# Usage: bash setup_conflict_backport.sh [target-dir]
# Prints:
#   REPO=<path>
#   SHA=<sha of commit to backport>

set -euo pipefail

REPO="${1:-/tmp/backport-eval-conflict}"
rm -rf "$REPO"
mkdir -p "$REPO"
cd "$REPO"

git init -q
git config user.email "test@example.com"
git config user.name "Test"

# Shared base
cat > config.go <<'EOF'
package main

const Timeout = 30
EOF
git add config.go
git commit -q -m "initial commit"

# stable-5.0 diverges: changes Timeout to 60
git checkout -q -b stable-5.0
cat > config.go <<'EOF'
package main

const Timeout = 60
EOF
git add config.go
git commit -q -m "chore: increase default timeout to 60s on stable"

# Back to main: changes Timeout to 45 (will conflict with stable's 60)
git checkout -q main 2>/dev/null || git checkout -q -b main
cat > config.go <<'EOF'
package main

const Timeout = 45
EOF
git add config.go
git commit -q -m "fix: set timeout to 45s"
SHA=$(git rev-parse HEAD)

echo "REPO=$REPO"
echo "SHA=$SHA"
