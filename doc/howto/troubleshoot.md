---
myst:
  html_meta:
    description: An index of how-to guides for troubleshooting common LXD issues, including firewall configuration, instance errors, and Dqlite problems.
---

(troubleshoot)=
# Troubleshooting

## Fix common issues

Commonly encountered issues include firewall conflicts (such as with Docker), instance errors, and Dqlite database problems.

```{toctree}
:titlesonly:

Configure your firewall </howto/network_bridge_firewalld>
Troubleshoot instances </howto/instances_troubleshoot>
Troubleshoot networks </howto/network_ipam>
Troubleshoot Dqlite </howto/dqlite_troubleshoot>
Frequently asked </faq>
```

## Dig deeper

LXD provides multiple debugging methods, including CLI tools and core dump files.

```{toctree}
:titlesonly:

Debug LXD </debugging>
```

If the issue cannot be resolved, see {ref}`support` for information about where to get help.
