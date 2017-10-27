test_clustering() {
  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  LXD_NETNS="${ns1}" spawn_lxd "${LXD_ONE_DIR}" false
  (
    set -e
    # shellcheck disable=SC2034
    LXD_DIR=${LXD_ONE_DIR}

  cat <<EOF | lxd init --preseed
config:
  core.trust_password: sekret
  core.https_address: 10.1.1.101:8443
  images.auto_update_interval: 15
storage_pools:
- name: data
  driver: dir
networks:
- name: $bridge
  type: bridge
  config:
    ipv4.address: none
    ipv6.address: none
profiles:
- name: default
  devices:
    root:
      path: /
      pool: data
      type: disk
cluster:
  name: one
EOF
  )

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/server.crt")

  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_TWO_DIR}"
  ns2="${prefix}2"
  LXD_NETNS="${ns2}" spawn_lxd "${LXD_TWO_DIR}" false
  (
    set -e
    # shellcheck disable=SC2034
    LXD_DIR=${LXD_TWO_DIR}

  cat <<EOF | lxd init --preseed
config:
  core.https_address: 10.1.1.102:8443
  images.auto_update_interval: 15
storage_pools:
- name: data
  driver: dir
networks:
- name: $bridge
  type: bridge
  config:
    ipv4.address: none
    ipv6.address: none
profiles:
- name: default
  devices:
    root:
      path: /
      pool: data
      type: disk
cluster:
  name: two
  target_address: 10.1.1.101:8443
  target_password: sekret
  target_cert: "$cert"
EOF
  )

  # The preseeded network bridge exists on all nodes.
  ip netns exec "${ns1}" ip link show "${bridge}" > /dev/null
  ip netns exec "${ns2}" ip link show "${bridge}" > /dev/null

  # The preseeded network can be deleted from any node, other nodes
  # are notified.
  LXD_DIR="${LXD_TWO_DIR}" lxc network delete "${bridge}"

  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  sleep 2
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"
}
