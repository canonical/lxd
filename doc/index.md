---
relatedlinks: https://ubuntu.com/lxd, https://ubuntu.com/blog/open-source-for-beginners-dev-environment-with-lxd
---

# LXD

LXD (<a href="#" title="Listen" onclick="document.getElementById('player').play();return false;">`[lɛks'di:]`&#128264;</a>) is a modern, secure and powerful system container and virtual machine manager.

<audio id="player">  <source src="_static/lxd.mp3" type="audio/mpeg">  <source src="_static/lxd.ogg" type="audio/ogg">  <source src="_static/lxd.wav" type="audio/wav"></audio>

% Include content from [../README.md](../README.md)
```{include} ../README.md
    :start-after: <!-- Include start LXD intro -->
    :end-before: <!-- Include end LXD intro -->
```

---

## In this documentation

`````{only} diataxis

````{grid} 1 1 2 2

```{grid-item} [Tutorials](tutorial/index)

**Start here**: a hands-on introduction to LXD for new users
```

```{grid-item} [How-to guides](howto/index)

**Step-by-step guides** covering key operations and common tasks
```

````

````{grid} 1 1 2 2
:reverse:

```{grid-item} [Reference](reference/index)

**Technical information** - specifications, APIs, architecture
```

```{grid-item} [Explanation](explanation/index)

**Discussion and clarification** of key topics
```

````

`````

```{filtered-toctree}
:titlesonly:
:maxdepth: 1

:topical:self
:topical:getting_started
:topical:Server and client <operation>
:topical:security
:topical:instances
:topical:images
:topical:storage
:topical:networks
:topical:projects
:topical:clustering
:topical:production-setup
:topical:migration
:topical:restapi_landing
:topical:Internals & debugging <internals>
:topical:external_resources
:topical:Switch to Diataxis navigation <https://documentation.ubuntu.com/lxd/en/latest/>
```

---

## Security

% Include content from [../README.md](../README.md)
```{include} ../README.md
    :start-after: <!-- Include start security -->
    :end-before: <!-- Include end security -->
```

See {ref}`security` for detailed information.

````{important}
% Include content from [../README.md](../README.md)
```{include} ../README.md
    :start-after: <!-- Include start security note -->
    :end-before: <!-- Include end security note -->
```
````

---

## Project and community

LXD is free software and developed under the [Apache 2 license](https://www.apache.org/licenses/LICENSE-2.0).
It’s an open source project that warmly welcomes community projects, contributions, suggestions, fixes and constructive feedback.

The LXD project is sponsored by [Canonical Ltd](https://www.canonical.com).

- [Code of Conduct](https://github.com/canonical/lxd/blob/main/CODE_OF_CONDUCT.md)
- [Contribute to the project](contributing.md)
- [Release announcements](https://discourse.ubuntu.com/c/lxd/news/)
- [Release tarballs](https://github.com/canonical/lxd/releases/)
- [Get support](support.md)
- [Watch tutorials and announcements on YouTube](https://www.youtube.com/c/LXDvideos)
- [Discuss on IRC](https://web.libera.chat/#lxd) (see [Getting started with IRC](https://discourse.ubuntu.com/t/getting-started-with-irc/37907) if needed)
- [Ask and answer questions on the forum](https://discourse.ubuntu.com/c/lxd/)

```{filtered-toctree}
:hidden:
:titlesonly:

:diataxis:self
:diataxis:tutorial/index
:diataxis:howto/index
:diataxis:explanation/index
:diataxis:reference/index
:diataxis:Switch to topical navigation <https://documentation.ubuntu.com/lxd/to/latest/>
```
