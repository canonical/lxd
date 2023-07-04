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

## Ubuntu Security disclosure and embargo policy

See the [Ubuntu Security disclosure and embargo
policy](https://ubuntu.com/security/disclosure-policy) for information
about how to contact the Ubuntu Security Team, what you can expect when
you contact us, and what we expect from you.
