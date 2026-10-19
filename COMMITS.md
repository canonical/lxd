# Commit format

All commits must be signed off (`git commit -s`) and cryptographically signed.
See [GitHub's documentation on commit signature verification](https://docs.github.com/en/authentication/managing-commit-signature-verification).

## Prefix table

Use a prefix that reflects the primary directory or area changed:

| Type                 | Affects files                                    | Commit message format               |
|----------------------|--------------------------------------------------|-------------------------------------|
| **API extensions**   | `doc/api-extensions.md`, `shared/version/api.go` | `api: Add XYZ extension`            |
| **Documentation**    | Files in `doc/`                                  | `doc: Update XYZ`                   |
| **API structure**    | Files in `shared/api/`                           | `shared/api: Add XYZ`               |
| **Go client package**| Files in `client/`                               | `client: Add XYZ`                   |
| **CLI changes**      | Files in `lxc/`                                  | `lxc/<command>: Change XYZ`         |
| **LXD daemon**       | Files in `lxd/`                                  | `lxd/<package>: Add support for XYZ`|
| **Snap packaging**   | Files in `snap/`                                 | `snap/<part>: Change XYZ` or `snap/local/<dir>: Change XYZ` |
| **Tests**            | Files in `test/`                                 | `test/<path>: Add test for XYZ`     |
| **GitHub**           | Files in `.github/`                              | `github: Update XYZ`                |
| **Makefile**         | `Makefile`                                       | `Makefile: Update XYZ`              |

Depending on complexity, large changes should be split into smaller, logical commits to facilitate review and backporting.

## Message format

- Keep the subject line to 72 characters or fewer.
- Separate the subject from the body with a blank line.
- Wrap every body line to at most 72 characters. Git does not wrap for you, so set your editor's text width to 72 or wrap by hand.
- Leave lines that cannot be broken without losing meaning, such as URLs and pasted command output, unwrapped.

## Signing and attribution

`Signed-off-by` is a Developer Certificate of Origin (DCO) certification — a legal assertion that you wrote or have the right to submit the code. Only a human submitter can make this certification. AI agents must not add a `Signed-off-by` line.

A `pre-push` hook (`.githooks/pre-push`) rejects pushes that contain commits without a sign-off or a signature. Enable it once per clone with `git config core.hooksPath .githooks`. See [CONTRIBUTING.md](CONTRIBUTING.md) for details.
