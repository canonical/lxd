---
myst:
  html_meta:
    description: Add, view, and edit custom CLI shortcuts in LXD using lxc alias.
---

(lxc-alias)=
# How to add command aliases

```{note}
Command aliases are a concept in the LXD CLI.
They are not applicable to the UI or API.
```

The LXD command-line client supports adding aliases for commands that you use frequently.
You can use aliases as shortcuts for longer commands, or to automatically add flags to existing commands.

To manage command aliases, you use the [`lxc alias`](lxc_alias.md) command.

For example, to always ask for confirmation when deleting an instance, create an alias for `lxc delete` that always runs `lxc delete -i`:

    lxc alias add delete "delete -i"

To see all configured aliases, run [`lxc alias list`](lxc_alias_list.md).

To [view all aliases in YAML format](lxc_alias_show.md) (useful for exporting or inspection), run:

```bash
lxc alias show
```

To [edit all aliases](lxc_alias_edit.md) interactively or via file input, run:

```bash
lxc alias edit
```

This command opens your system's default text editor and allows you to modify all aliases at once.

You can also pipe alias configurations to this command. Examples:

```bash
# Export aliases to a file
lxc alias show > aliases.yaml

# Import aliases from a file
lxc alias edit < aliases.yaml

# Import from pipe
cat aliases.yaml | lxc alias edit
```

Run [`lxc alias --help`](lxc_alias.md) to see all available subcommands.
