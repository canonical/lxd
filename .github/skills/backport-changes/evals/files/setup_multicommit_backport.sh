#!/usr/bin/env bash
# Sets up a minimal git repo for eval 2: multi-commit PR-style backport.
# Simulates two commits that together constitute a logical change, like a PR.
# Usage: bash setup_multicommit_backport.sh [target-dir]
# Prints:
#   REPO=<path>
#   SHA1=<first commit sha>
#   SHA2=<second commit sha>

set -euo pipefail

REPO="${1:-/tmp/backport-eval-multi}"
rm -rf "$REPO"
mkdir -p "$REPO"
cd "$REPO"

git init -q
git config user.email "test@example.com"
git config user.name "Test"

# Shared base
cat > api.go <<'EOF'
package main

func GetVersion() string {
    return "1.0"
}
EOF
git add api.go
git commit -q -m "initial commit"

# stable-5.0 branches from here
git checkout -q -b stable-5.0
git checkout -q main 2>/dev/null || git checkout -q -b main

# First commit of the "PR" on main
cat > api.go <<'EOF'
package main

func GetVersion() string {
    return "1.1"
}
EOF
git add api.go
git commit -q -m "feat: bump version to 1.1"
SHA1=$(git rev-parse HEAD)

# Second commit of the "PR" on main
cat > changelog.md <<'EOF'
# Changelog

## v1.1
- Bumped version to 1.1
EOF
git add changelog.md
git commit -q -m "docs: add changelog entry for v1.1"
SHA2=$(git rev-parse HEAD)

echo "REPO=$REPO"
echo "SHA1=$SHA1"
echo "SHA2=$SHA2"
