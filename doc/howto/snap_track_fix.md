(snap-track-bugfix)=
# Track a bugfix in the LXD snap

Given a bug report that has been fixed in LXD, we can determine which snap channels have the fix and which ones don't.

For more information about managing the LXD snap, see {ref}`howto-snap` and {ref}`ref-releases-snap`.

The strategy used to track the fix will depend on both the snap's {ref}`risk level <ref-snap-risk>` (`edge` vs `candidate`/`stable`) and its {ref}`release type <ref-releases>`.

The LXD snap packaging is maintained directly in the LXD repository ([canonical/lxd](https://github.com/canonical/lxd)) under the `snap/` and `snapcraft/` directories. (Packaging files used to be maintained separately, until the `canonical/lxd-pkg-snap` repository was merged with the LXD repository.)

As an example, consider the issue [canonical/lxd#18023](https://github.com/canonical/lxd/issues/18023); this bug was [introduced in LXD 3.0](https://github.com/canonical/lxd/commit/d840004886b702b3bea15d4a1d6e4f32717a6b62). The linked pull request points to commit [`c1e8ab4`](https://github.com/canonical/lxd/commit/c1e8ab4c33c217a200e59f27916fd8e1d49241e2) as the fix.

Use `snap info` to show the currently available versions of LXD:

```{terminal}
:user: ubuntu
:host: ubuntu

snap info lxd

channels:
  5.21/stable:      5.21.4-aee7e08 2026-04-09 (38767)  123MB -
  5.21/candidate:   5.21.4-4189d16 2026-05-05 (39296)  123MB -
  5.21/beta:        ↑
  5.21/edge:        git-658877c    2026-05-07 (39329)  120MB -
  latest/stable:    6.7-736f34a    2026-04-03 (38768)  121MB -
  latest/candidate: 6.8-8470555    2026-05-06 (39313)  121MB -
  latest/beta:      ↑
  latest/edge:      git-3c2c6c6    2026-05-08 (39363)  119MB -
  6/stable:         6.7-736f34a    2026-04-03 (38768)  121MB -
  6/candidate:      6.8-8470555    2026-05-06 (39313)  121MB -
  6/beta:           ↑
  6/edge:           git-3c2c6c6    2026-05-08 (39363)  119MB -
```

```{note}
The `latest/*` and `6/*` channels are equivalent in the above snap output; they point to the same snap revision. The strategies shown below for `latest/*` can be used unmodified for the current {ref}`feature release <ref-releases-feature>` channels (`6/*` in this case).
```

## Feature releases

### `latest/edge` channel

The snap version numbers for all `edge` {ref}`risk levels <ref-snap-risk>` include the commit hash from [canonical/lxd](https://github.com/canonical/lxd) that was used to build that snap revision. To check if `latest/edge` contains the bug fix, clone [canonical/lxd](https://github.com/canonical/lxd) and check if the fix commit (`c1e8ab4`) is an ancestor of the commit used to build the snap (`3c2c6c6`):

```{terminal}
:user: ubuntu
:host: ubuntu

git merge-base --is-ancestor c1e8ab4 3c2c6c6 && echo "c1e8ab4 reachable from 3c2c6c6"

c1e8ab4 reachable from 3c2c6c6
```

This means that the fix is present in the `latest/edge` channel.

(ref-troubleshoot-snap-track-stable)=
### `latest/candidate` and `latest/stable` channels

The commit hash shown in the snap version string for `candidate` and `stable` risk levels corresponds to the Git commit of the repository at build time. For current releases, this is a commit in [canonical/lxd](https://github.com/canonical/lxd). (For older revisions built before the snap packaging repository was merged, the hash refers to a commit that was originally in `canonical/lxd-pkg-snap`.)

In [canonical/lxd](https://github.com/canonical/lxd), check if the fix commit (`c1e8ab4`) is an ancestor of the commit in the `latest/candidate` version string (`6.8-8470555`):

```{terminal}
:user: ubuntu
:host: ubuntu

git merge-base --is-ancestor c1e8ab4 8470555 && echo "c1e8ab4 reachable from 8470555"

c1e8ab4 reachable from 8470555
```

This means that the fix is present in the `latest/candidate` channel. To check if the fix is present in `latest/stable`, follow the same procedure as above using the corresponding version string (`6.7-736f34a`).

```{note}
For older snap revisions built before `canonical/lxd-pkg-snap` was merged into `canonical/lxd`, critical fixes may have been cherry-picked at build time by `git` commands executed during the snap build process. Check `yq '.parts["lxd"]["override-build"]' snapcraft.yaml` in [canonical/lxd-pkg-snap](https://github.com/canonical/lxd-pkg-snap) to see if a fix was cherry-picked.
```

## LTS releases

Bugfixes are frequently backported to {ref}`LTS releases <ref-releases-snap>`; backports do not use the same commit hash and may not even have exactly the same code. Use `git log` to determine if the fix is present in the `5.21/edge` channel by checking the `stable-5.21` branch in [canonical/lxd](https://github.com/canonical/lxd) for commits containing the issue number (`#18023`) or the original commit hash (`c1e8ab4`):

```{terminal}
:user: ubuntu
:host: ubuntu

git log --grep="#18023" origin/stable-5.21

commit cce42965ad1e3b6a20995dc2c7a6a73ac2246944
Author: Thomas Parrott <thomas.parrott@canonical.com>
Date:   Wed Apr 15 16:04:40 2026 +0100

    lxd/networks: Only take networkCreateLock for external API requests

    This avoids deadlocks with the operation notification when multiple concurrent network creation requests arrive at different cluster members.

    Fixes #18023

    Signed-off-by: Thomas Parrott <thomas.parrott@canonical.com>
```

[`c1e8ab4`](https://github.com/canonical/lxd/commit/c1e8ab4c33c217a200e59f27916fd8e1d49241e2) was backported as [`cce4296`](https://github.com/canonical/lxd/commit/cce42965ad1e3b6a20995dc2c7a6a73ac2246944).

Follow the same strategy used for `stable` and `candidate` {ref}`feature releases <ref-troubleshoot-snap-track-stable>` to determine if [`cce4296`](https://github.com/canonical/lxd/commit/cce42965ad1e3b6a20995dc2c7a6a73ac2246944) is available in the `5.21/candidate` and `5.21/stable` channels.
