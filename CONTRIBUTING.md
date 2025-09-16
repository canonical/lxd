# Contributing

<!-- Include start contributing -->

The LXD team welcomes contributions through pull requests, issue reports, and discussions.
- Contribute to the code or documentation, report bugs, or request features in the [GitHub repository](https://github.com/canonical/lxd)
- Ask questions or join discussions in the [LXD forum](https://discourse.ubuntu.com/c/lxd/126).

Review the following guidelines before contributing to the project.

## Code of Conduct

All contributors must adhere to the [Ubuntu Code of Conduct](https://ubuntu.com/community/ethos/code-of-conduct).

## License and copyright

All contributors must sign the [Canonical contributor license agreement (CCLA)](https://canonical.com/legal/contributors), which grants Canonical permission to use the contributions.

- You retain copyright ownership of your contributions (no copyright assignment).
- By default, contributions are licensed under the project's **AGPL-3.0-only** license.
- Exceptions:
  - Canonical may import code under AGPL-3.0-only compatible licenses, such as Apache-2.0.
  - Such code retains its original license and is marked as such in commit messages or file headers.
  - Some files and commits are licensed under Apache-2.0 rather than AGPL-3.0-only. These are indicated in their package-level COPYING file, file header, or commit message.

## Pull requests

Submit pull requests on GitHub at: [`https://github.com/canonical/lxd`](https://github.com/canonical/lxd).

All pull requests undergo review and must be approved before being merged into the main branch.

### Commit structure

Use separate commits for different types of changes:

| Type                 | Affects files                                    | Commit message format               |
|----------------------|--------------------------------------------------|-------------------------------------|
| **API extensions**   | `doc/api-extensions.md`, `shared/version/api.go` | `api: Add XYZ extension`            |
| **Documentation**    | Files in `doc/`                                  | `doc: Update XYZ`                   |
| **API structure**    | Files in `shared/api/`                           | `shared/api: Add XYZ`               |
| **Go client package**| Files in `client/`                               | `client: Add XYZ`                   |
| **CLI changes**      | Files in `lxc/`                                  | `lxc/<command>: Change XYZ`         | 
| **LXD daemon**       | Files in `lxd/`                                  | `lxd/<package>: Add support for XYZ`|
| **Tests**            | Files in `tests/`                                | `tests: Add test for XYZ`           |

Depending on complexity, large changes might be further split into smaller, logical commits. This commit structure facilitates the review process and simplifies backporting fixes to stable branches.

### Developer Certificate of Origin sign-off

To ensure transparency and accountability in contributions to this project, all contributors must include a **Signed-off-by** line in their commits in accordance with DCO 1.1:

```
Developer Certificate of Origin
Version 1.1

Copyright (C) 2004, 2006 The Linux Foundation and its contributors.
660 York Street, Suite 102,
San Francisco, CA 94110 USA

Everyone is permitted to copy and distribute verbatim copies of this
license document, but changing it is not allowed.

Developer's Certificate of Origin 1.1

By making a contribution to this project, I certify that:

(a) The contribution was created in whole or in part by me and I
    have the right to submit it under the open source license
    indicated in the file; or

(b) The contribution is based upon previous work that, to the best
    of my knowledge, is covered under an appropriate open source
    license and I have the right under that license to submit that
    work with modifications, whether created in whole or in part
    by me, under the same open source license (unless I am
    permitted to submit under a different license), as indicated
    in the file; or

(c) The contribution was provided directly to me by some other
    person who certified (a), (b) or (c) and I have not modified
    it.

(d) I understand and agree that this project and the contribution
    are public and that a record of the contribution (including all
    personal information I submit with it, including my sign-off) is
    maintained indefinitely and may be redistributed consistent with
    this project or the open source license(s) involved.
```

#### Including a Signed-off-by line in your commits

Every commit must include a **Signed-off-by** line, even when part of a larger set of contributions. To do this, use the `-s` flag when committing:

    git commit -s -m "Your commit message"

This automatically adds the following to your commit message:

```
Signed-off-by: Your Name <your.email@example.com>
```

By including this line, you acknowledge your agreement to the DCO 1.1 for that specific contribution.

- Use a valid name and email address—anonymous contributions are not accepted.
- Ensure your email matches the one associated with your GitHub account.

### Commit signature verification

In addition to the sign-off requirement, contributors must also cryptographically sign their commits to verify authenticity. See: [GitHub's documentation on commit signature verification](https://docs.github.com/en/authentication/managing-commit-signature-verification).

### Make-generated files

Some changes require regenerating certain files using Makefile commands.

After you run any of the commands below, you'll be prompted whether to commit the changes. If you respond `Y`, only the re-generated files are committed—any other staged files are ignored.

#### Formatting

If you modify any Go source files, format them:

	make update-fmt

#### CLI tool string updates

If you modify CLI strings in `lxc/`, regenerate and commit translation files:

    make i18n

#### API updates

If you modify the LXD API (`shared/api`), regenerate and commit the Swagger YAML file (`doc/rest-api.yaml`) used for API reference documentation:

    make update-api

#### Configuration options updates

If you add or update configuration options, regenerate and commit the documentation metadata files (`lxd/metadata/configuration.json` and `doc/metadata.txt`):

    make update-metadata

#### Development environment setup

Several pieces of software are needed in order to build and test LXD. Here is an easy way to create a virtual-machine to use as a development environment. LXD itself is needed to power that virtual-machine so install it first: {ref}`installing`.

Once LXD is installed and {ref}`initialized <initialize>`, a special profile (`lxd-test`) needs to be loaded. The profile requires includes a `lxd-git` device (see {ref}`devices-disk-types` for details) that will share LXD's git repository with the virtual-machine. Since this path is specific to your environment you need to adjust it when loading the profile:

```sh
# this needs to be run from inside the git repostory
GIT_ROOT="$(git rev-parse --show-toplevel)"
# create or edit the profile based on the provided template
lxc profile list | grep -qwF lxd-test || lxc profile create lxd-test
sed "s|@@PATH_TO_LXD_GIT@@|${GIT_ROOT}|" "${GIT_ROOT}/doc/lxd-test.yaml" | lxc profile edit lxd-test
```

The `lxd-test` profile assigns CPU and memory limits similar to those available in free GitHub Action runners. Those can be adapted to the specifications of a more modest physical machine:

```sh
lxc profile set lxd-test limits.cpu=2
lxc profile set lxd-test limits.memory=4GiB
lxc profile device set lxd-test root size=8GiB
```

This profile can then be used to launch an Ubuntu Noble VM and start using it:

```sh
lxc launch ubuntu-minimal-daily:24.04 v1 --vm -p lxd-test
sleep 30
# this may take a while as many packages need to be installed
lxc exec v1 -- cloud-init status --wait --long
```

Then it is possible to build all the dependencies, LXD binaries and even run tests either automatically or manually:

```sh
# start a root shell in the VM
lxc exec v1 -- bash

# go into the git repo
cd lxd

# build deps and LXD binaries
make deps && make

# get an interactive test shell session with all the needed environment variables to use and test LXD
make test-shell

# run the `exec` and `query` tests
./main.sh exec
./main.sh query

# or manually interact with LXD, for example:
lxc launch ubuntu:24.04 u1
lxc exec u1 -- hostname
lxc delete --force u1

# for a barebones test instance with just busybox (note: no IP automatically configured)
./deps/import-busybox --alias testimage
lxc launch testimage c1
```

At this point you might want to learn more on {doc}`debugging`.

#### Updating Copilot instruction file

The LXD repository includes a [Copilot instructions file](https://github.com/canonical/lxd/blob/main/.github/copilot-instructions.md) to improve Copilot Code Review responses. When updating this file, include concise context about LXD's architecture, coding standards, and best practices. Clear guidance helps Copilot produce accurate, relevant suggestions. For details and tips, see the documentation on [GitHub Copilot repository custom instructions](https://docs.github.com/en/copilot/how-tos/configure-custom-instructions/add-repository-instructions#about-repository-custom-instructions-for-copilot).

<!-- Include end contributing -->

## More information

For more information, including details about contributing to the code as well as the documentation for LXD, see [How to contribute to LXD](https://documentation.ubuntu.com/lxd/latest/contributing/) in the documentation.
