# Security policy
## Supported versions
<!-- Include start supported versions -->

LXD has two types of releases:
 - Monthly feature releases
 - LTS releases

For feature releases, only the latest one is supported, and we usually
don't do point releases. Instead, users are expected to wait until the
next monthly release.

For LTS releases, we do periodic bugfix releases that include an
accumulation of bugfixes from the feature releases. Such bugfix releases
do not include new features.

<!-- Include end supported versions -->

## What qualifies as a security issue
We don't consider privileged containers to be root safe, so any exploit
allowing someone to escape them will not qualify as a security issue.
This doesn't mean that we're not interested in preventing such escapes,
but we simply do not consider such containers to be root safe.

Unprivileged container escapes are certainly something we'd consider a
security issue, especially if somehow facilitated by LXD.

More details can be found here: https://linuxcontainers.org/lxc/security/

## Reporting a vulnerability
The easiest way to report a security issue is by e-mail to:
 security@linuxcontainers.org

This e-mail address will reach the three main maintainers for LXC/LXD/LXCFS:
 - Christian Brauner
 - Stéphane Graber
 - Serge Hallyn

We will be working with you to determine whether the issue qualifies as a
security issue, if so in what component and then handle figuring out a
fix, getting a CVE assigned and coordinating the release of the fix to
the various Linux distributions.
