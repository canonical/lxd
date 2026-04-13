#!/usr/bin/env bash
# Sets up a minimal git repo for eval 1: single-commit backport.
# Usage: bash setup_simple_backport.sh [target-dir]
# Prints the repo path and commit SHA to stdout as:
#   REPO=<path>
#   SHA=<sha>

set -euo pipefail

REPO="${1:-/tmp/backport-eval-simple}"
rm -rf "$REPO"
mkdir -p "$REPO"
cd "$REPO"

git init -q
git config user.email "test@example.com"
git config user.name "Test"

# Shared base on main
cat > buggy.go <<'EOF'
package main

import "fmt"

func main() {
    fmt.Println("hello")
}
EOF
git add buggy.go
git commit -q -m "initial commit"

# Create stable-5.0 from the base
git checkout -q -b stable-5.0

git checkout -q main 2>/dev/null || git checkout -q -b main

# Add a bug-fix commit on main
cat > buggy.go <<'EOF'
package main

import "fmt"

func main() {
    fmt.Println("hello, world")
}
EOF
git add buggy.go
git commit -q -m "fix: correct greeting message"

SHA=$(git rev-parse HEAD)

echo "REPO=$REPO"
echo "SHA=$SHA"
