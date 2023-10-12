# How to get support

<!-- Include start release -->

LXD maintains different release branches in parallel.

Long term support (LTS) releases
: The current LTS releases are LXD 5.0.x (snap channel `5.0/stable`) and LXD 4.0.x (snap channel `4.0/stable`).

  The LTS releases follow the Ubuntu release schedule and are released every two years:

  - LXD 5.0 is supported until June 2027 and gets frequent bugfix and security updates, but does not receive any feature additions.
    Updates to this release happen approximately every six months, but this schedule should be seen as a rough estimation that can change based on priorities and discovered bugs.
  - LXD 4.0 is supported until June 2025.
  - LXD 6.0 is planned for April 2024 and will be supported until June 2029.

Feature releases
: The current feature release is LXD 5.x.
  It is available through the snap channels `latest/stable`, `latest/candidate`, and `latest/edge`, in addition to channels for the most recent specific releases (for example, `5.18/stable`).
  See `sudo snap info lxd` for a full list of available channels.

  Feature releases are pushed out about every month and contain new features as well as bugfixes.
  The normal support length for those releases is until the next release comes out.
  Some Linux distributions might offer longer support for particular feature releases that they decided to ship.

<!-- Include end release -->

% Include content from [../README.md](../README.md)
```{include} ../README.md
    :start-after: <!-- Include start support -->
    :end-before: <!-- Include end support -->
```
