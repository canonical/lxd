# Evals for the `backport-changes` skill

These evals were designed using the
[skill-creator](https://www.skills.sh/anthropics/skills/skill-creator) framework
from Anthropic, which provides a structured approach to writing, running, and
grading agent evaluations.

## What the evals test

| # | Name | What it exercises |
|---|------|-------------------|
| 1 | Simple backport | Single commit, clean cherry-pick onto a stable branch |
| 2 | Multi-commit backport | Two commits applied in topological order |
| 3 | Conflict handling | Conflict that requires abort and clear user explanation |
| 4 | Mixed PR (cosmetic + essential) | Triage of cosmetic vs essential commits; cosmetic conflict skipped, essential conflict resolved |

## How to run an eval

Each eval is self-contained. The setup scripts create a throwaway git repo
(or worktree) so there is no risk of corrupting the real repository.

**Step 1 — Set up the test repo:**

```bash
# Example for eval 4 (mixed commits):
bash .github/skills/backport-changes/evals/files/setup_mixed_backport.sh
# Outputs: REPO=... BRANCH=... COMMITS=...
```

**Step 2 — Run the skill against the eval prompt:**

Use the Copilot CLI and paste the prompt from `evals.json` (substituting the
values printed by the setup script):

```
# In the Copilot CLI terminal session, e.g.:
> Backport these 4 commits onto stable-5.0 in /tmp/backport-eval-mixed: ...
```

The skill (`SKILL.md`) is automatically loaded by the Copilot CLI when the
`backport-changes` skill is active.

**Step 3 — Check the result:**

```bash
cd /tmp/backport-eval-mixed   # or whatever REPO was printed
git log --oneline -10
git status -s   # should be empty
```

Compare against the `expectations` in `evals.json` for that eval.

## Real-world PR test

A separate setup script (`files/setup_real_backport.sh`) creates a worktree
of the actual LXD repository at the pre-backport stable-5.21 tip (commit
`9aaba5c`) for testing against PR #18180. This requires the LXD repo to be
available locally:

```bash
bash .github/skills/backport-changes/evals/files/setup_real_backport.sh /path/to/lxd-repo
```

> **Note:** This eval exercises 18 real commits with genuine Go merge conflicts
> and takes 20–50 minutes. For regular regression testing, eval 4 (synthetic)
> is a faster equivalent.
