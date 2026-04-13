---
name: backport-changes
description: >
  Use this skill when the user asks to backport commits or a PR from a newer
  branch to an older stable release branch (e.g. "backport this fix to
  stable-5.0"). Triggers on phrases like: "backport this PR", "cherry-pick to
  stable-X.X", "backport PR #1234 to stable-5.21".
---

## Overview

Backporting carries a fix or feature from a newer branch (e.g. `main` or `stable-5.21`) to an older release branch (e.g. `stable-5.0`) using `git cherry-pick -x`. The `-x` flag appends the source commit SHA to the cherry-pick commit message, preserving provenance.

## Steps

### 1. Identify the commits

If the user provides a PR number or URL, resolve it to commits using the `gh` CLI:

```bash
# Get the list of commit SHAs in a PR in chronological order
gh pr view <pr-number> --json commits --jq '.commits[].oid'
```

If `gh` is not available, fall back to searching the git history for the merge commit:

```bash
git log --all --grep="(#<pr-number>)" --oneline
```

If the user provides commit hashes directly, use those instead.

Collect the commit hashes to backport in topological order (parent before child).

```bash
# List commits in a PR in topological order (parent first), ready to cherry-pick
git --no-pager log --oneline --reverse --topo-order <base>..<head>
```

### 2. Prepare the target branch

```bash
git fetch origin
git checkout <target-branch>
git pull origin <target-branch>
git status -s  # must output nothing before proceeding
```

### 3. Apply the commits

The simplest approach is `git cherry-pick -x`, which automatically appends the `(cherry picked from commit ...)` marker:

```bash
git cherry-pick -x <commit-hash>
```

Multiple commits can be applied in a single invocation:

```bash
git cherry-pick -x <commit-hash-1> <commit-hash-2> ...
```

Any other method is also acceptable as long as each resulting commit includes the `(cherry picked from commit <original-sha>)` trailer in its message.

### 4. Resolve conflicts (if any)

When `git cherry-pick` reports conflicts:

1. Inspect the conflicting files: `git status` and `git diff`
2. Edit each file to resolve, preserving the **intent** of the original
   commit while respecting patterns already in the target branch (e.g.
   keep the target branch's dependency versions unless the backport
   explicitly updates them)
3. Stage the resolved files: `git add <file>`
4. Complete the cherry-pick: `git cherry-pick --continue`

If a conflict cannot be resolved cleanly, abort and report:

```bash
git cherry-pick --abort
```

Then explain what blocked the backport and what manual intervention is needed.

### 5. Verify

```bash
git log --oneline -10   # confirm cherry-picked commits are present
git status -s           # must output nothing (clean working tree)
```

## Key rules

- Every backported commit **must** include a `(cherry picked from commit <sha>)` trailer — `git cherry-pick -x` adds this automatically
- **Never** force-push or rewrite history on release branches
- **Stop and report** if a conflict cannot be resolved; do not skip ahead
