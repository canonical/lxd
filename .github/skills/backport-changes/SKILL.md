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

### 3. Triage the commits (for multi-commit PRs)

Before applying anything, scan the commit list and classify each commit:

- **Essential** — functional changes that directly implement the fix or feature (logic changes, new behaviour, bug fixes)
- **Cosmetic / optional** — pure rename/rewording of error messages, whitespace fixes, comment-only changes, or style cleanups that are independent of the fix

Apply essential commits first. Cosmetic commits can be skipped if they conflict — they carry no semantic value and omitting them keeps the backport minimal and safe. If you skip a commit, note it explicitly in your summary.

### 4. Apply the commits

Apply commits one at a time in topological order so that a conflict on one commit
does not cascade into subsequent ones:

```bash
git cherry-pick -x <commit-hash>
```

### 5. Resolve conflicts (if any)

When `git cherry-pick` reports conflicts:

1. Determine whether the conflicting commit is **essential** or **cosmetic/optional**:
   - If **cosmetic/optional**: abort immediately and move on — do not read any files.
     ```bash
     git cherry-pick --abort
     ```
   - If **essential**: read only the conflict markers, not the entire file. Use
     `git diff` to see just the conflicting hunks, then edit the minimum necessary
     to preserve the intent of the original commit while respecting the target
     branch's existing patterns. Stage and continue:
     ```bash
     git diff          # read conflict markers only
     # edit the file to resolve
     git add <file>
     git cherry-pick --continue
     ```

2. **Empty cherry-picks** — if git reports "The previous cherry-pick is now empty",
   the change was already absorbed by a prior conflict resolution. Skip it:
   ```bash
   git cherry-pick --skip
   ```

3. **Do not attempt the same conflict resolution more than twice.** If an essential
   commit is still unresolved after a second attempt, abort and stop:
   ```bash
   git cherry-pick --abort
   ```
   Explain precisely which file/hunk conflicted and what the user must do manually.
   Stopping early with a clear report is always better than spinning.

### 6. Verify

```bash
git log --oneline -10   # confirm cherry-picked commits are present
git status -s           # must output nothing (clean working tree)
git diff --check        # catch any leftover conflict markers
```

Do **not** run build or test commands (`go test`, `make`, etc.) unless the user
explicitly asks for them. Verification ends at a clean working tree.

## Key rules

- Every backported commit **must** include a `(cherry picked from commit <sha>)` trailer — `git cherry-pick -x` adds this automatically
- **Never** force-push or rewrite history on release branches
- **Stop and report early** if an essential commit's conflict cannot be resolved in two attempts; do not spin indefinitely
- **Cosmetic-only commits that conflict may be skipped** — always note any skipped commits in your summary so the user knows exactly what was included
