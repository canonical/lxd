---
relatedlinks: '[Run&#32;system&#32;containers&#32;with&#32;LXD](https://canonical.com/lxd), [Open&#32;source&#32;for&#32;beginners:&#32;setting&#32;up&#32;your&#32;dev&#32;environment&#32;with&#32;LXD](https://ubuntu.com/blog/open-source-for-beginners-dev-environment-with-lxd)'
---

# LXD

LXD (<a href="#" title="Listen" onclick="document.getElementById('player').play();return false;">`[lɛks'di:]`&#128264;</a>) is a modern, secure and powerful system container and virtual machine manager.

<audio id="player"><source src="_static/lxd.mp3" type="audio/mpeg"></audio>

% Include content from [../README.md](../README.md)
```{include} ../README.md
    :start-after: <!-- Include start LXD intro -->
    :end-before: <!-- Include end LXD intro -->
```

---

```{rst-class} hclass2
```

```{rubric} In this documentation
```

`````{only} diataxis

````{grid} 1 1 2 2

```{grid-item-card} [Tutorials](tutorial/index)

**Start here**: a hands-on introduction to LXD for new users, guiding you through your first steps using the CLI or the UI
- {ref}`first-steps`
- {ref}`tutorial-ui`
```

```{grid-item-card} [How-to guides](howto/index)

**Step-by-step guides** covering key operations and common tasks
- {ref}`howtos-getstarted`
- {ref}`howtos-work`
- {ref}`howtos-production`
```

````

````{grid} 1 1 2 2
:reverse:

```{grid-item-card} [Reference](reference/index)

**Technical information**
- {ref}`reference-general`
- {ref}`reference-config`
- {ref}`reference-production`
- {ref}`reference-api`
- {ref}`reference-manpages`
- {ref}`reference-internal`
```

```{grid-item-card} [Explanation](explanation/index)

**Discussion and clarification** of key topics
- {ref}`explanation-concepts`
- {ref}`explanation-entities`
- {ref}`explanation-iam`
- {ref}`explanation-production` (including {ref}`Security <security>`)
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
```

---

```{rst-class} hclass2
```

```{rubric} Project and community
```

LXD is free software and released under [AGPL-3.0-only](https://www.gnu.org/licenses/agpl-3.0.en.html) (it may contain some contributions that are licensed under the Apache-2.0 license, see [License and copyright](contributing)).
It’s an open source project that warmly welcomes community projects, contributions, suggestions, fixes and constructive feedback.

The LXD project is sponsored by [Canonical Ltd](https://canonical.com/).

- [Code of Conduct](https://github.com/canonical/lxd/blob/main/CODE_OF_CONDUCT.md)
- [Contribute to the project](contributing.md)
- [Release announcements](https://discourse.ubuntu.com/c/lxd/news/143)
- [Release tarballs](https://github.com/canonical/lxd/releases/)
- [Get support](support.md)
- [Watch tutorials and announcements on YouTube](https://www.youtube.com/c/LXDvideos)
- [Discuss on IRC](https://web.libera.chat/#lxd) (see [Getting started with IRC](https://discourse.ubuntu.com/t/getting-started-with-irc/37907) if needed)
- [Ask and answer questions on the forum](https://discourse.ubuntu.com/c/lxd/126)

```{filtered-toctree}
:hidden:
:titlesonly:

:diataxis:self
:diataxis:tutorial/index
:diataxis:howto/index
:diataxis:explanation/index
:diataxis:reference/index
```
