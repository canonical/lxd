# LXD documentation

The LXD documentation is available at: https://linuxcontainers.org/lxd/docs/latest/

GitHub provides a basic rendering of the documentation as well, but important features like includes and clickable links are missing. Therefore, we recommend reading the [published documentation](https://linuxcontainers.org/lxd/docs/latest/).

## Documentation format

The documentation is written in [Markdown](https://commonmark.org/) with [MyST](https://myst-parser.readthedocs.io/) extensions.

For syntax help and guidelines, see the [documentation cheat sheet](https://linuxcontainers.org/lxd/docs/latest/doc-cheat-sheet/) ([source](doc-cheat-sheet.md?plain=1)).

## Building the documentation

To build the documentation, run `make doc` from the root folder of the repository. This command installs the required tools and renders the output to the `doc/html/` folder. To update the documentation for changed files only (without re-installing the tools), run `make doc-incremental`.

After building, run `make doc-serve` and go to http://localhost:8001 to view the rendered documentation.
