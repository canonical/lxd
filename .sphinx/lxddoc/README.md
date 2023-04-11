# lxddoc

A small CLI to parse comments in a Golang codebase meant to be used for a documentation tool (like Sphinx for example).

It parses the comments from the AST and extract their 'Doc Code' and 'Doc Comment'. The comments can be filtered by doc codes. The doc code is thought to be used as a key to template the documentation with its comment value.

# Usage

    $ lxddoc -h
    Usage of lxddoc:
    -e value
        Path that will be excluded from the process
    -t string
        Path to the root of the templates (e.g, 'lxd/doc')

# Formatting

A comment is formatted this way:

```go
    // :lxddoc(<DOC CODE>)<message>
```

# Examples
## Basic

Assume the following file:

```go
var InstanceConfigKeysAny = map[string]func(value string) error{
	// :lxddoc(InstanceConfig.volatile.apply_template) The name of a template hook that should be triggered upon next startup
    "volatile.apply_template":         validate.IsAny, // (at the end of a line...) :lxddoc(InstanceConfig.volatile.apply_nvram)
    // Whether to regenerate VM NVRAM the next time the instance starts
	"volatile.apply_nvram": validate.Optional(validate.IsBool),
    /* (it even works with long note comment and MAJ 'LXDDOC' keyword)
    *
    :LXDDOC(InstanceConfig.volatile.base_image.blabla)
    The hash of the image the instance was created from (if any)
    */
    "volatile.base_image":             validate.IsAny,
    // ...
}
```

For example, the following markdown file with templates :


Key                       | Type      | Default           | Live update   | Condition                 | Description
:--                       | :---      | :------           | :----------   | :----------               | :----------
`volatile.apply_template` | string    | {{ :lxddoc(InstanceConfig.volatile.apply_template) }}
`volatile.apply_nvram`    | string    | {{&#32;&#32;&#32;:lxddoc(InstanceConfig.volatile.apply_template)&#32;&#32;}} <!-- spaces inside the template don't matter -->
`volatile.base_image`     | string    | {{:lxddoc(InstanceConfig.volatile.base_image.blabla)}}

Will be shown as the following :

Key                       | Type      | Default           | Live update   | Condition                 | Description
:--                       | :---      | :------           | :----------   | :----------               | :----------
`volatile.apply_template` | string    | The name of a template hook that should be triggered upon next startup
`volatile.apply_nvram`    | string    | Whether to regenerate VM NVRAM the next time the instance starts
`volatile.base_image`     | string    | The hash of the image the instance was created from (if any)

## Justification of the tool and integration in our documentation pipeline

This command should be executed **before** building the Sphinx documentation (a.k.a `sphinx build ...`). Unfortunately, the Sphinx AutoAPI plugin for Go could have been helpful for us, but it is no longer maintained nor supported for Sphinx v3 and higher. This sub ~300LoC tool is doing nearly the same while avoiding a python integration module.