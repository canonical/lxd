(support)=
# How to get support

<!-- Include start release -->

LXD maintains different release branches in parallel.

Long term support (LTS) releases
: The current LTS releases are LXD 5.21.x (snap channel `5.21/stable` - this is the default channel) and LXD 5.0.x (snap channel `5.0/stable`).

  An LTS release starts its life with full support, then moves to maintenance support, and finally to extended support. OS vendors might provide additional support[^1].

  - Full support: some new features, frequent bugfixes, and security updates are provided.
  - Maintenance support: high impact bugfixes and security updates are provided.
  - Extended support: only high and critical security updates are provided.

  The LTS releases follow the Ubuntu release schedule and are released every two years:

  - LXD 5.21 is supported until June 2029.
    It gets frequent bugfix and security updates, but does not receive any feature additions.
    Updates to this release happen approximately every six months, but this schedule should be seen as a rough estimation that can change based on priorities and discovered bugs.
    Currently in full support phase.
  - LXD 5.0 is supported until June 2027.
    Currently in maintenance support phase.

Feature releases
: The current feature series is LXD 6.x (starting with 6.1).
  This is available from the `6/stable` snap channel and  will continue to follow the LXD 6.x series as it
  progresses from a feature release series into an LTS release in 2026.
  See `snap info lxd` for a full list of available channels.

  Feature releases are pushed out more often and contain new features as well as bugfixes.
  The normal support length for those releases is until the next release comes out.
  Some Linux distributions might offer longer support for particular feature releases that they decided to ship.

[^1]: Canonical provides additional support through the [Ubuntu Pro](https://ubuntu.com/pro) offering.

<!-- Include end release -->

% Include content from [../README.md](../README.md)
```{include} ../README.md
    :start-after: <!-- Include start support -->
    :end-before: <!-- Include end support -->
```
