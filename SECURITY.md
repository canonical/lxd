# Security policy

## Supported releases
<!-- Include start supported versions -->

LXD has two types of releases:

- **Feature releases** receive updates with each new release. We do not do point
  releases; instead, users are expected to wait for the next feature release.
- **LTS releases** receive two years of full support followed by three years of
  maintenance support. Full support includes bugfixes and security updates, and
  maintenance support includes high-impact bugfixes and critical security
  updates.

<!-- Include end supported versions -->

For additional details about releases and support levels, see
[Releases and snap](https://canonical.com/lxd/docs/latest/reference/releases-snap/).

## What qualifies as a security vulnerability

Security vulnerabilities are issues that allow someone to bypass security
boundaries enforced by LXD, on deployments and hosts configured according to
our security recommendations (see
[How to harden security for LXD](https://canonical.com/lxd/docs/latest/howto/security_harden/)
and [Security](https://canonical.com/lxd/docs/latest/explanation/security/)).
For example:

- Unprivileged container escapes, to the host or another instance, especially if
  facilitated by LXD.
- Access or modification of resources outside a project to which a user is
  restricted, when all project features are enabled or when privileges are
  managed via fine-grained authorization. See
  [Project features](https://canonical.com/lxd/docs/latest/reference/projects/#project-features).

Exploits that rely on LXD or host configurations that do not conform to our
security recommendations are not considered security vulnerabilities. For
example:

- Exploits that require AppArmor to be disabled, since we strongly recommend the
  use of an AppArmor-enabled host for any production deployment. See
  [Use a host with AppArmor enabled](https://canonical.com/lxd/docs/latest/howto/security_harden/#use-a-host-with-apparmor-enabled).
- Exploits that allow someone to escape privileged containers will be treated as
  ordinary bugs, since we don't consider privileged containers to be root-safe.
  See [Privileged containers](https://canonical.com/lxd/docs/latest/explanation/security/#privileged-containers).
- Exploits that require project features to be disabled, since projects can
  only be considered a safe boundary for tenant isolation if configured with
  appropriate limits and with all features enabled (unless privileges are
  managed via fine-grained authorization).

## How to report a vulnerability

You can
[submit a vulnerability report](https://github.com/canonical/lxd/security/advisories/new)
through GitHub. See
[Privately reporting a security vulnerability](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing/privately-reporting-a-security-vulnerability)
for instructions.

Please adhere to the following when submitting your report:

- Provide a concise, readable summary of the vulnerability.
- Validate the vulnerability against the `edge` risk level of a snap track
  and/or the corresponding branch of the LXD repo. The `main` branch corresponds
  to the `latest/edge` channel, and the `stable-{series}` branches correspond to
  each of the `{series}/edge` channels. For example, to validate a vulnerability
  against LXD 6.10._z_ LTS, use the `6/edge` snap channel and/or the `stable-6`
  branch.
- Include any proof of concept or reproducer steps, if possible.
- Keep your report scoped to a single vulnerability, meaning a single root
  cause. If you discover multiple vulnerabilities, please submit a separate
  report for each.

After receiving your report, the LXD GitHub admins will determine whether the
issue qualifies as a security vulnerability and, if so, in which component. The
LXD team will then coordinate a fix, CVE assignment, and security release.
Depending on the severity of the vulnerability, the LXD team will backport fixes
to relevant series in accordance with our release and support policy (see
[Releases and snap](https://canonical.com/lxd/docs/latest/reference/releases-snap/)).

We treat vulnerability reports on a first-come, first-served basis, and give
credit to reporters in our CVE assignments.

The [Ubuntu Security disclosure and embargo policy](https://ubuntu.com/security/disclosure-policy)
contains more information about what you can expect when you contact us, and
what we expect from you.
