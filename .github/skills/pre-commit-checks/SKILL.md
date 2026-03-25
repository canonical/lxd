---
name: pre-commit-checks
description: >
  Use this skill when finalizing a bug fix or new feature before committing:
  after code changes are complete in lxd/, lxc/, shared/, or client/; before
  proposing a commit or pull request; or when asked to validate, lint, or check
  the build.
---

## When to run

Run the pre-commit checklist after finalizing any code change — bug fix, new
feature, or refactor — before committing. Do not skip checks because the change
looks small; static analysis and unit tests frequently catch issues that are not
obvious from reading the diff.

## Checklist

Run these in order. Each step must pass before moving to the next.

### 1. Static analysis

```bash
make static-analysis
```

Runs `golangci-lint`, `errortype`, and `zerolint`. This step may reformat files
or generate output — review any modifications before staging them. Only keep
generated files that are relevant to your changes.

### 2. Unit tests

```bash
make check-unit
```

Runs all Go unit tests with coverage. Fix any failures before proceeding.

### 3. Build

```bash
make
```

Confirms the full binary set compiles cleanly.

### 4. Documentation (if docs changed)

```bash
make doc-html
```

Only required if files under `doc/` were modified.

## Important notes

- `make static-analysis` may modify files (e.g. formatting). Always review and
  stage only the changes relevant to your work; discard any unrelated noise.
- If a check fails and leaves behind temporary files, clean them up before
  committing.
- Fix failures rather than suppressing them. Only add `//nolint` or shellcheck
  disable directives when the warning is a confirmed false positive, and always
  include a comment explaining why.
