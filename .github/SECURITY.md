# Security policy
## Supported versions
LXD has two type of releases:
 - Monthly feature releases
 - LTS releases

For feature releases, only the latest one is supported and we usually
won't be doing point releases on those, instead just having our users
wait until the next monthly release.

For LTS releases, we do periodic bugfix releases which includes an
accumulation of bugfixes from the feature releases, no new features are
included.

## What qualify as a security issue
We don't consider privileged containers to be root safe, so any exploit
allowing someone to escape them, will not qualify as a security issue.
This doesn't mean that we're not interested in preventing such escapes
but we simply do not consider such containers to be root safe.

Unprivileged container escapes are certainly something we'd consider a
security issue, especially if somehow facilitated by LXD.

More details can be found here: https://linuxcontainers.org/lxc/security/

## Reporting a vulnerability
The easiest way to report a security issue is to e-mail: security@linuxcontainers.org

This e-mail address will reach the three main maintainers for LXC/LXD/LXCFS:
 - Christian Brauner
 - Stéphane Graber
 - Serge Hallyn

We will be working with you to determine whether this does qualify as a
security issue, if so in what component and then handle figuring out a
fix, getting a CVE assigned and coordinating the release of the fix to
the various Linux distributions.
