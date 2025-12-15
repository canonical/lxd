## Summary

<!-- Describe what this PR changes and why -->

## Related Issues

Fixes #
Closes #
Related to #

## Breaking Changes

<!-- If this PR introduces breaking changes, describe them here and update documentation accordingly -->

None

## API Extensions

<!-- If this PR adds new API functionality, list the API extension(s) added -->

None

## Checklist

### Commit Requirements
- [ ] I have read the [contributing guidelines](https://github.com/canonical/lxd/blob/main/CONTRIBUTING.md) and attest that all commits in this PR are [signed off](https://github.com/canonical/lxd/blob/main/CONTRIBUTING.md#including-a-signed-off-by-line-in-your-commits), [cryptographically signed](https://github.com/canonical/lxd/blob/main/CONTRIBUTING.md#commit-signature-verification), and follow this project's [commit structure](https://github.com/canonical/lxd/blob/main/CONTRIBUTING.md#commit-structure).

### Testing
- [ ] I have successfully built the project with `make`.
- [ ] I have run `make static-analysis` and resolved any issues.
- [ ] I have run `make check-unit` and all tests pass.
- [ ] I have added or updated unit tests for my changes (if applicable).
- [ ] I have considered whether integration tests in `test/suites/` need updates (if applicable).

### Documentation
- [ ] I have checked and added or updated relevant documentation in `doc/`.
- [ ] I have run `make update-metadata` (if configuration options changed).
- [ ] I have run `make update-api` (if `shared/api` changed).

### Generated Files
- [ ] I have run necessary `make update-*` commands for any generated files affected by my changes (see [Make-generated files](https://github.com/canonical/lxd/blob/main/CONTRIBUTING.md#make-generated-files)).
