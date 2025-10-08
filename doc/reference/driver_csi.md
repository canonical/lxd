(ref-csi)=
# LXD CSI driver reference

This document contains reference information for the LXD CSI driver CLI, including available Helm chart values and its versioning information.

(ref-csi-cli)=
## CLI

The `lxd-csi-driver` provides CSI controller and node server functionality.
You can configure runtime options using these flags:

Flag               | Default                 | Description
-------------------|-------------------------|------------
`--driver-name`    | `lxd.csi.canonical.com` | CSI driver name
`--endpoint`       | `unix:///tmp/csi.sock`  | Internal CSI Unix socket path
`--devLXDEndpoint` | `unix:///dev/lxd/sock`  | DevLXD Unix socket path
`--nodeID`         | `""`                    | Kubernetes node ID
