# LXD

LXD is a modern, secure and powerful system container and virtual machine manager.

This is the snap packaging repository that is used to build the [LXD snap](https://snapcraft.io/lxd). The LXD repository is available [here](https://github.com/canonical/lxd).

# Build the LXD snap locally

Local build require the LXD snap to be installed as `snapcraft` creates a container to use as build environment. Here's how to do a local build for the native architecture:

```
snapcraft pack
```

# Build the LXD snap on Launchpad

To build the snap for multiple architectures, Launchpad builders can be used.

They are available for various architectures (`amd64`, `armhf`, `arm64`, `ppc64el`, `riscv64` and `s390x`) and you can ask for multiple to be built in parallel. Here's how to build for both `amd64` and `arm64`:

```
snapcraft remote-build --launchpad-accept-public-upload --build-for amd64,arm64
```

## GitHub Actions

### `lp-snap-build` action

The `.github/actions/lp-snap-build` composite action sets up SSH access to Launchpad and clones the snap packaging repository in preparation for pushing a build trigger commit.

It also builds and installs the `lxd-snapcraft` helper tool (from `.github/actions/lp-snap-build/lxd-snapcraft/`) which can read and update `version` and `source-commit` fields in `snapcraft.yaml`.

#### Inputs

| Input | Required | Description |
| :--- | :--- | :--- |
| `ssh-key` | yes | Private ed25519 SSH key for Launchpad access and commit signing |

#### Required environment variables

| Variable | Description |
| :--- | :--- |
| `SSH_AUTH_SOCK` | Path to the SSH agent socket |
| `REPO` | URL of the Launchpad snap packaging repository |
| `BRANCH` | Branch to clone from the Launchpad repository |
| `PACKAGE` | Name of the snap package (used for the clone directory `~/${PACKAGE}-pkg-snap-lp`) |

### Usage from another repo

```yaml
jobs:
  snap:
    runs-on: ubuntu-24.04
    env:
      SSH_AUTH_SOCK: /tmp/ssh_agent.sock
      PACKAGE: "lxd"
      REPO: "git+ssh://lxdbot@git.launchpad.net/~lxd-snap/lxd"
      BRANCH: ${{ github.ref_name }}
    steps:
      - uses: actions/checkout@...
        with:
          persist-credentials: false

      - uses: canonical/lxd-pkg-snap/.github/actions/lp-snap-build@main
        with:
          ssh-key: ${{ secrets.LAUNCHPAD_LXD_BOT_KEY }} # zizmor: ignore[secrets-outside-env]

      - name: Trigger Launchpad snap build
        run: |
          localRev="$(git rev-parse HEAD)"
          ver=($(lxd-snapcraft -package "${PACKAGE}" -get-version -file ~/"${PACKAGE}-pkg-snap-lp/snapcraft.yaml"))
          rsync -a --exclude .git --delete . ~/"${PACKAGE}-pkg-snap-lp"/
          cd ~/"${PACKAGE}-pkg-snap-lp"
          lxd-snapcraft -package "${PACKAGE}" -set-version "${ver[0]}" -set-source-commit "${ver[1]}"
          git add --all
          git commit --all -s --allow-empty -m "Automatic upstream build (${BRANCH})" -m "Upstream commit: ${localRev}"
          git push
```
