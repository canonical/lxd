---
relatedlinks: "[LXD's&#32;development&#32;process&#32;-&#32;YouTube](https://www.youtube.com/watch?v=pqV0Z1qwbkg)"
---

# How to contribute to LXD

% Include content from [../CONTRIBUTING.md](../CONTRIBUTING.md)
```{include} ../CONTRIBUTING.md
    :start-after: <!-- Include start contributing -->
    :end-before: <!-- Include end contributing -->
```

## Contribute to the code

Follow the steps below to set up your development environment and start working on new LXD features.

### Install LXD from source

To build the dependencies, follow the instructions in {ref}`installing_from_source`.

### Add your fork as a remote

After setting up your build environment, add your GitHub fork as a remote and fetch the latest updates:

    git remote add myfork git@github.com:<your_username>/lxd.git
    git remote update

Then switch to the main branch of your fork:

    git switch myfork/main

### Build LXD

Now you can build your fork of the project by running:

    make

Before making changes, create a new branch on your fork:

```bash
git switch -c <name_of_your_new_branch>
```

Set up tracking for the new branch to make future pushes easier:

```bash
git push -u myfork <name_of_your_new_branch>
```

### Important notes for new LXD contributors

- Persistent data is stored in the `LXD_DIR` directory, which is created by running `lxd init`.
   - By default, `LXD_DIR` is located at `/var/lib/lxd` (for non-snap installations) or `/var/snap/lxd/common/lxd` (for snap users).
   - To prevent version conflicts, consider setting a separate `LXD_DIR` for your development fork.
- Binaries compiled from your source are placed in `$(go env GOPATH)/bin` by default.
   - When testing, explicitly invoke these binaries instead of the global `lxd` you might have installed.
   - For convenience, you can create an alias in your `~/.bashrc` to call these binaries with the appropriate flags.
- If you have a `systemd` service running LXD from a previous installation, consider disabling it to prevent version conflicts with your development build.

## Contribute to the documentation

We strive to make LXD as easy and straightforward to use as possible. To achieve this, our documentation aims to provide the information users need, cover all common use cases, and answer typical questions.

You can contribute to the documentation in several ways. We appreciate your help!

### Ways to contribute

Document new features or improvements you contribute to the code.
: - Submit documentation updates in pull requests alongside your code changes. We will review and merge them together with the code.

Clarify concepts or common questions based on your own experience.
: - Submit a pull request with your documentation improvements.

Report documentation issues by opening an issue on [GitHub](https://github.com/canonical/lxd/issues).
: - We will evaluate and update the documentation as needed.

Ask questions or suggest improvements in the [LXD forum](https://discourse.ubuntu.com/c/lxd/126).
: - We monitor discussions and update the documentation when necessary.

Join discussions in the `#lxd` channel on IRC via [Libera Chat](https://web.libera.chat/#lxd).
: - While we cannot guarantee responses to IRC posts, we monitor the channel and use feedback to improve the documentation.

If you contribute images to `doc/images`:
- Use **SVG** or **PNG** formats.
- Optimize PNG images for smaller file size using a tool like [TinyPNG](https://tinypng.com/) (web-based), [OptiPNG](https://optipng.sourceforge.net/) (CLI-based), or similar.

% Include content from [README.md](README.md)
```{include} README.md
    :start-after: <!-- Include start docs -->
```

When you open a pull request, a preview of the documentation hosted by Read the Docs is built automatically.
To see this, view the details for the `docs/readthedocs.com:canonical-lxd` check on the pull request. Others can also use this preview to validate your changes.

### Automatic documentation checks

GitHub runs automatic checks on the documentation to verify the spelling, the validity of links, correct formatting of the Markdown files, and the use of inclusive language.

You can (and should!) run these tests locally before pushing your changes:

- Check the spelling: `make doc-spellcheck` (or `make doc-spelling` to first build the documentation and then check it)
- Check the validity of links: `make doc-linkcheck`
- Check the Markdown formatting: `make doc-lint`
- Check for inclusive language: `make doc-woke`

### Document instructions (how-to guides)

LXD can be used with different clients, primarily the command-line interface (CLI), API, and UI.
The documentation contains instructions for all of these, so when adding or updating how-to guides, remember to update the documentation for all clients.

#### Using tabs for client-specific information

When instructions differ between clients, use tabs to organize them:

`````
````{tabs}
```{group-tab} CLI
[...]
```
```{group-tab} API
[...]
```
```{group-tab} UI
[...]
```
````
`````

```{tip}
You might need to increase the number of backticks (`) if there are code blocks or other directives in the tab content.
```

#### Guidelines for writing instructions

CLI instructions
: - Link to the relevant `lxc` command reference. Example: ``[`lxc init`](lxc_init.md)``
  - You don't need to document all available command flags, but mention any that are especially relevant.
  - Examples are very helpful, so add a few if it makes sense.

API instructions
: - When possible, use [`lxc query`](lxc_query.md) to demonstrate API calls.
    For complex calls, use `curl` or other widely available tools.
  - In the request data, include all required fields but keep it minimal—there's no need to list every possible field.
  - Link to the API call reference. Example: ``[`POST /1.0/instances`](swagger:/instances/instances_post)``

UI instructions
: - Use screenshots sparingly—they are difficult to keep up to date.
  - When referring to labels in the UI, use the `{guilabel}` role.
    Example: ``To create an instance, go to the {guilabel}`Instances` section and click {guilabel}`Create instance`.``

### Document configuration options

Configuration options are documented by comments in the Go code. These comments are extracted automatically.

#### Adding or modifying configuration options

- Look for comments that start with `lxdmeta:generate` in the code.
- When adding or modifying a configuration option, include the corresponding documentation comment.
- Refer to the [`lxd-metadata` README file](https://github.com/canonical/lxd/blob/main/lxd/lxd-metadata/README.md) for formatting guidelines.
- When you add or modify configuration options, you must re-generate `doc/metadata.txt` and `lxd/metadata/configuration.json`. See the [](#configuration-options-updates) section for instructions.

#### Including configuration options in documentation

The documentation pulls sections from `doc/metadata.txt` to display a group of configuration options.
For example, to include the core server options, use:

````
% Include content from [metadata.txt](metadata.txt)
```{include} metadata.txt
    :start-after: <!-- config group server-core start -->
    :end-before: <!-- config group server-core end -->
```
````

#### When to update documentation files

- If you add a new option to an existing group, no changes to the documentation files are needed, aside from [re-generating `metadata.txt`](#configuration-options-updates). The option will be included automatically.
- If you define a new group, to add it to the documentation, you must add an `{include}` directive to the appropriate Markdown file in `doc/`.
