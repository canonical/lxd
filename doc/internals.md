---
myst:
  html_meta:
    description: An index of reference information for LXD internals, including daemon behavior, syscall interception, namespaces, OVN implementation, live migration, and Dqlite.
---

# Internals

These reference guides document the internal workings of LXD and are primarily intended for contributors and developers.

## Runtime behavior

The LXD client and daemon can use environment variables for paths, proxies, and advanced features. Daemon startup, shutdown, and signal handling are also documented here.

```{toctree}
:titlesonly:

/environment
/daemon-behavior
/reference/uefi_variables
```

## Security and isolation

Specific system calls from containers can be intercepted and handled safely by the LXD daemon. User namespaces use UID/GID idmaps to isolate containers from the host.

```{toctree}
:titlesonly:

/syscall-interception
User namespace setup </userns-idmap>
```

## Subsystem internals

```{toctree}
:titlesonly:

OVN implementation </reference/ovn-internals>
VM live migration implementation </reference/vm_live_migration_internals>
Dqlite database for cluster state </reference/dqlite-internals>
ZFS storage driver </reference/storage_zfs_internals>
```

## Related topics

How-to guides:

- {ref}`troubleshoot`
