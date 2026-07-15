---
description: "Draft LXD release notes from a validated commit range and update the release-notes index. Use when asked to prepare release notes for a version/branch."
name: "Release Notes"
argument-hint: "version=<x.y[.z]> branch=<name> start=<commit> end=<commit> is_lts=<yes|no> ui_tour_url=<optional URL>"
agent: "agent"
---
## Purpose

Generate LXD release notes using
doc/reference/release-notes/release-notes-template.md as the base, with strict
input validation and repository-specific formatting.

The workflow must collect required inputs, validate the commit range on the
requested branch, draft release-notes content from commits in range, write the
release-notes file, and update the relevant series index.

## Input argument mapping

Use user-provided arguments when present:
- version
- branch
- start (maps to start_commit)
- end (maps to end_commit)
- is_lts (yes or no)
- ui_tour_url (optional)

## Required inputs

Ask for all of the following before generating content:

- version (for example: 6.10, 5.21.6, 5.0.8)
- branch (git ref resolvable by `git rev-parse --verify`; for example: main, stable-5.21, origin/stable-5.21, lxd-6.10)
- start_commit (exclusive lower bound for commit range; not included in start..end)
- end_commit (inclusive upper bound for commit range)
- is_lts (yes or no)
- ui_tour_url (optional Discourse release announcement URL for the LXD UI tour sentence)

Do not proceed on missing required inputs (version, branch, start_commit,
end_commit, is_lts). Ask for ui_tour_url as well; if it is
unavailable, continue and omit the LXD UI tour sentence.

## Validation

Run strict validation before drafting any notes:

- Confirm branch is a git ref that `git rev-parse --verify <branch>` can resolve (for example: local branch, tag, or remote-tracking ref).
- Confirm start_commit and end_commit resolve to full commits.
- Confirm both commits are reachable from the specified branch.
- Confirm start_commit is an ancestor of end_commit.
- Confirm the range is valid and non-empty.

Recommended command checks:

- git rev-parse --verify <branch>
- git rev-parse --verify <start_commit>^{commit}
- git rev-parse --verify <end_commit>^{commit}
- git merge-base --is-ancestor <start_commit> <branch>
- git merge-base --is-ancestor <end_commit> <branch>
- git merge-base --is-ancestor <start_commit> <end_commit>
- git rev-list --count <start_commit>..<end_commit>

Fail fast if validation fails. Do not generate release notes with ambiguous or
invalid commit ranges.

## Workflow

1. Collect required inputs.
2. Validate branch, start_commit, and end_commit strictly.
3. Gather commits in range and supporting metadata (subject, PR links, touched
   paths when needed for categorization).
4. Identify all new API extensions introduced in the commit range by checking
   changes to `doc/api-extensions.md` and searching for references to
   `extension-` API names in commit messages, PR descriptions, and code changes
   (particularly in `lxd/api_*.go` files and related handlers).
5. Start from the release notes template and replace placeholders for the
   requested version.
6. Populate sections based on commit content, then prune sections that are not
   relevant. Ensure every identified API extension is documented either:
   - In the **Highlights** section with a descriptive subsection and
     `{ref}`extension-name`` link to `doc/api-extensions.md`, OR
   - In the **Backwards-incompatible changes** section if the extension
     introduces incompatible behavior changes.
7. Write the release-notes file in the correct series directory.
8. Update the matching series index.md to include the new note at the top of
   the toctree list.
9. Run final quality checks before returning the result.

## Style and structure rules

### Release type sentence

Use the existing wording style exactly:

- If is_lts=yes, include:
  This is a {ref}`LTS release <ref-releases-lts>` and is recommended for production use.
- If is_lts=no, include:
  This is a {ref}`feature release <ref-releases-feature>` and is not recommended for production use.

Do not include both sentences.

### LTS maintenance style

For LTS releases, follow the current style in existing LTS notes:

- Include a short maintenance paragraph after the release-content admonition. Example pattern: "This is a maintenance release for the X.Y LTS series. It ... backported from the main development branch."
- Keep tone factual and concise.

### Sections

Use template headings/anchors and keep only relevant sections:

- Always include: Highlights, Bug fixes, Change log, Downloads.
- Include only when supported by commit evidence: UI updates, Backwards-incompatible changes, Deprecated features, Updated minimum Go version, Snap packaging changes.
- Remove placeholder headings and comments that are not applicable.

### Highlights references

For each Highlights subsection, add links to relevant API extensions and/or
documentation pages when such references are available from the commits, PRs,
or existing docs.

- Prefer direct links to `doc/api-extensions.md` anchors for API extension names.
- Add at least one supporting documentation link per highlight when relevant docs exist.
- If no relevant API extension or documentation reference is available for a specific highlight, do not invent one.

### API extension coverage

Every new API extension introduced in the release must be documented in the
release notes:

- Identify all new `extension-` entries by examining changes to
  `doc/api-extensions.md` (the authoritative source for API extensions) and
  cross-referencing with commit messages and code changes.
- Each new API extension must appear in either the **Highlights** section
  (preferred for new features) or **Backwards-incompatible changes** section
  (for breaking changes in the extension).
- Removed API extensions must be documented in the **Backwards-incompatible
  changes** section to alert users of the removal.
- When documenting an extension in Highlights, include a descriptive subsection
  explaining the feature and link to the extension anchor:
  `{ref}`extension-name``.
- If an API extension is in the commit range but no highlight or
  backwards-incompatible documentation exists for it, create a brief subsection
  in Highlights explaining what the extension provides.
- Always reference API extensions by linking to their definition in
  `doc/api-extensions.md` using the `{ref}`extension-name`` format.

### Content quality

- Prefer human-readable summaries over raw commit text.
- Deduplicate repeated fixes.
- Keep claims verifiable from commits/linked PRs/issues.
- For security/CVE/GHSA content, avoid adding claims not supported by source references.
- If a PR number is unavailable in commit metadata, link to the commit URL instead of omitting the source reference.
- In the Bug fixes section, format each linked bullet label using the existing style: [{spellexception}`Bug fix description`](https://...).
- Keep CVE/GHSA identifiers inside the {spellexception} wrapped label when present.
- If a bug fix has a GitHub Security Advisory reference (for example a github.com/<org>/<repo>/security/advisories/GHSA-... URL), use the advisory URL as the bug-fix link instead of a PR or commit link.
- Do not include a GitHub Security Advisory item in the Bug fixes section if it was already listed in the release notes of the previous release in the same series. Check the most recent release notes file in the same series directory before adding any GHSA entry.
- Link priority for Bug fixes entries is: Security Advisory URL > PR URL > commit URL.
- Order the Bug fixes list so entries linked to GitHub Security Advisories appear first, followed by PR-linked entries, then commit-linked entries.
- For non-security-advisory bug fixes (PR/commit links), order entries chronologically with the oldest fixes first.

## File placement and indexing

Determine series directory from version:

- 4.0.z -> doc/reference/release-notes/4.0/
- 5.0.z -> doc/reference/release-notes/5.0/
- 5.21.z -> doc/reference/release-notes/5.21/
- 6.x -> doc/reference/release-notes/6/

Write the release note as:

- doc/reference/release-notes/<series>/release-notes-<version>.md

Then update:

- doc/reference/release-notes/<series>/index.md

Add LXD <version> <release-notes-<version>> as the first entry in the toctree.

## Links and placeholders

- Set change-log compare link from the requested range. Prefer release tags when available (lxd-a.b...lxd-x.y) to match existing style; otherwise use <start_commit>...<end_commit>.
- Set downloads link to https://github.com/canonical/lxd/releases/tag/lxd-<version>.
- Set snap channel in Downloads to the series track (4.0/stable, 5.0/stable, 5.21/stable, 6/stable).
- Ask for a Discourse release announcement URL for the LXD UI tour sentence.
- Include the LXD UI sentence in the release-notes-content admonition only when ui_tour_url is provided, and set the sentence link from that URL.
- If ui_tour_url is not provided, omit the LXD UI sentence.

## Final checks

Before finishing, verify:

- No leftover x.y, a.b, or placeholder text remains.
- Anchors and title consistently use the requested version.
- Release type sentence matches is_lts.
- The release note is linked from the correct series index.
- Compare/download links are valid for the provided range/version.
- All new API extensions from the commit range are documented in either
  **Highlights** or **Backwards-incompatible changes** sections.
- Each documented API extension includes a link to its anchor in
  `doc/api-extensions.md` (format: `{ref}`extension-name``).
- Highlights include links to relevant API extensions and documentation when
  such links are available.
- If ui_tour_url is provided, the LXD UI Discourse sentence is present in the admonition and links to that URL.
- If ui_tour_url is not provided, the LXD UI sentence is omitted.

## Output requirements

- Include created/updated file paths.
- Include the exact compare link and downloads link used.
- Call out if ui_tour_url was omitted and therefore the UI tour sentence was not included.
