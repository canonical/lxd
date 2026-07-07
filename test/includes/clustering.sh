# Test helper for clustering

setup_clustering_bridge() {
  local name="br$$"

  echo "==> Setup clustering bridge ${name}"

  ip link add "${name}" up type bridge
  ip addr add 100.64.1.1/16 dev "${name}"

  iptables -w -t nat -A POSTROUTING -s 100.64.0.0/16 -d 0.0.0.0/0 -j MASQUERADE
  echo 1 > /proc/sys/net/ipv4/ip_forward
}

teardown_clustering_bridge() {
  local name="br$$"

  [ -e "/sys/class/net/${name}" ] || return

  echo "==> Teardown clustering bridge ${name}"
  echo 0 > /proc/sys/net/ipv4/ip_forward
  iptables -w -t nat -D POSTROUTING -s 100.64.0.0/16 -d 0.0.0.0/0 -j MASQUERADE
  ip link del dev "${name}"
}

setup_clustering_netns() {
  local id="${1}"
  # shellcheck disable=SC2154
  local ns="${prefix}${id}"
  local ns_dir="${TEST_DIR}/ns/${ns}"
  local veth1="v${ns}1"
  local veth2="v${ns}2"
  local nsbridge="br$$"
  local netns_link="/run/netns/${ns}"

  echo "==> Setup clustering netns ${ns}"

  TEST_DIR="${TEST_DIR}" ns="${ns}" ns_dir="${ns_dir}" unshare -m -n /bin/sh <<'EOF'
set -e
mkdir -p "${ns_dir}"
touch "${ns_dir}/net"
mount -o bind /proc/self/ns/net "${ns_dir}/net"
mount --move /sys /mnt
umount -l /proc
mount -t sysfs sysfs /sys
mount --move /mnt/fs/cgroup /sys/fs/cgroup
mount -t proc proc /proc
mount -t securityfs securityfs /sys/kernel/security
umount -l /mnt

# Setup host netns access
mkdir -p /run/netns
mount -t tmpfs tmpfs /run/netns
touch /run/netns/hostns
mount --bind /proc/1/ns/net /run/netns/hostns

mount -t tmpfs tmpfs /usr/local/bin
cat <<'EOE' > /usr/local/bin/in-hostnetns
#!/bin/sh
exec ip netns exec hostns /usr/bin/$(basename "$0") "$@"
EOE
chmod +x /usr/local/bin/in-hostnetns
# Setup ceph
ln -s in-hostnetns /usr/local/bin/ceph
ln -s in-hostnetns /usr/local/bin/rbd

sleep 300 & echo $! > "${ns_dir}/PID"
EOF

  local nspid
  nspid="$(< "${ns_dir}/PID")"

  ip -batch - <<EOF
link add ${veth1} type veth peer name ${veth2}
link set dev ${veth2} netns ${nspid}
link set dev ${veth1} master ${nsbridge}
link set dev ${veth1} up
EOF
  mkdir -p /run/netns
  ln -snf "/proc/${nspid}/ns/net" "${netns_link}"
  ip -n "${ns}" -batch - <<EOF
link set dev lo up
link set dev ${veth2} name eth0
link set dev eth0 up
addr add 100.64.1.10${id}/16 dev eth0
route add default via 100.64.1.1
link add localBridge${id} type bridge
EOF
}

teardown_clustering_netns() {
  [ -d "${TEST_DIR}/ns/" ] || return 0

  local ns veth1

  # shellcheck disable=SC2045
  for ns in $(ls -1 "${TEST_DIR}/ns/"); do
      echo "==> Teardown clustering netns ${ns}"

      veth1="v${ns}1"
      if [ -e "/sys/class/net/${veth1}" ]; then
        ip link del "${veth1}"
      fi

      kill -9 "$(< "${TEST_DIR}/ns/${ns}/PID")" 2>/dev/null || true

      umount -l "${TEST_DIR}/ns/${ns}/net" >/dev/null 2>&1 || true
      rm -Rf "${TEST_DIR}/ns/${ns}"
      rm -f "/run/netns/${ns}"
  done
}

spawn_lxd_and_bootstrap_cluster() {
  local driver="${1:-dir}"
  local port="${2:-}"
  local index="${3:-1}"

  if [ ! -e "/sys/class/net/br$$" ]; then
    setup_clustering_bridge
  fi

  setup_clustering_netns "${index}"
  # shellcheck disable=SC2154
  local ns="${bridge}${index}"

  # Avoid overwriting the global LXD_DIR
  local LXD_DIR
  if [ "${LXD_DIR_KEEP:-""}" = "" ]; then
    LXD_DIR="$(mktemp -d -p "${TEST_DIR}" XXX)"
  else
    LXD_DIR="${LXD_DIR_KEEP}"
    mkdir -p "${LXD_DIR}"
    unset LXD_DIR_KEEP
  fi
  echo "==> Spawn bootstrap cluster node in ${ns} with storage driver ${driver}"

  LXD_NETNS="${ns}" spawn_lxd "${LXD_DIR}" false

  case "${index}" in
    1)
      # shellcheck disable=SC2034
      LXD_ONE_DIR="${LXD_DIR}" ns1="${ns}";;
    2)
      # shellcheck disable=SC2034
      LXD_TWO_DIR="${LXD_DIR}" ns2="${ns}";;
    3)
      # shellcheck disable=SC2034
      LXD_THREE_DIR="${LXD_DIR}" ns3="${ns}";;
    4)
      # shellcheck disable=SC2034
      LXD_FOUR_DIR="${LXD_DIR}" ns4="${ns}";;
    5)
      # shellcheck disable=SC2034
      LXD_FIVE_DIR="${LXD_DIR}" ns5="${ns}";;
    6)
      # shellcheck disable=SC2034
      LXD_SIX_DIR="${LXD_DIR}" ns6="${ns}";;
    7)
      # shellcheck disable=SC2034
      LXD_SEVEN_DIR="${LXD_DIR}" ns7="${ns}";;
    8)
      # shellcheck disable=SC2034
      LXD_EIGHT_DIR="${LXD_DIR}" ns8="${ns}";;
    9)
      # shellcheck disable=SC2034
      LXD_NINE_DIR="${LXD_DIR}" ns9="${ns}";;
    *)
      echo "spawn_lxd_and_bootstrap_cluster: Unsupported index ${index}"
      false
      ;;
  esac

  local preseed
  preseed="config:
  core.https_address: 100.64.1.10${index}:8443"

  if [ "${port}" != "" ]; then
    preseed+="
  cluster.https_address: 100.64.1.10${index}:${port}"
  fi

  preseed+="
  images.auto_update_interval: 0
storage_pools:
- name: data
  driver: ${driver}"

    if [ "${driver}" = "btrfs" ]; then
    preseed+="
  config:
    size: 1GiB"
    fi

    if [ "${driver}" = "zfs" ]; then
    preseed+="
  config:
    size: 1GiB
    zfs.pool_name: lxdtest-$(basename "${TEST_DIR}")-${ns}"
    fi

    if [ "${driver}" = "lvm" ]; then
    preseed+="
  config:
    volume.size: 25MiB
    size: 1GiB
    lvm.vg_name: lxdtest-$(basename "${TEST_DIR}")-${ns}"
    fi

    if [ "${driver}" = "ceph" ]; then
    preseed+="
  config:
    ceph.osd.pool_name: lxdtest-$(basename "${TEST_DIR}")
    volume.size: 25MiB
    ceph.osd.pg_num: 16"
    fi

  preseed+="
networks:
- name: ${bridge}
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
  server_name: node${index}
  enabled: true"

  # Print the preseed for debugging purposes.
  echo "${preseed}"

  lxd init --preseed <<< "${preseed}"
}

spawn_lxd_and_join_cluster() {
  local cert="${1}"
  local index="${2}"
  local target="${3}"
  local token="${4}"
  if [ -d "${4}" ]; then
    token="$(LXD_DIR=${4} lxc cluster add --quiet "node${index}")"
  fi
  local driver="${5:-dir}"
  local port="${6:-8443}"
  local source="${7:-}"
  local source_recover="${8:-false}"

  [ "${LXD_NETNS_KEEP:-""}" = "" ] && setup_clustering_netns "${index}"

  # Avoid overwriting the global LXD_DIR
  local LXD_DIR
  if [ "${LXD_DIR_KEEP:-""}" = "" ]; then
    LXD_DIR="$(mktemp -d -p "${TEST_DIR}" XXX)"
  else
    LXD_DIR="${LXD_DIR_KEEP}"
    mkdir -p "${LXD_DIR}"
    unset LXD_DIR_KEEP
  fi
  ns="${bridge}${index}"

  case "${index}" in
    2)
      # shellcheck disable=SC2034
      LXD_TWO_DIR="${LXD_DIR}" ns2="${ns}";;
    3)
      # shellcheck disable=SC2034
      LXD_THREE_DIR="${LXD_DIR}" ns3="${ns}";;
    4)
      # shellcheck disable=SC2034
      LXD_FOUR_DIR="${LXD_DIR}" ns4="${ns}";;
    5)
      # shellcheck disable=SC2034
      LXD_FIVE_DIR="${LXD_DIR}" ns5="${ns}";;
    6)
      # shellcheck disable=SC2034
      LXD_SIX_DIR="${LXD_DIR}" ns6="${ns}";;
    7)
      # shellcheck disable=SC2034
      LXD_SEVEN_DIR="${LXD_DIR}" ns7="${ns}";;
    8)
      # shellcheck disable=SC2034
      LXD_EIGHT_DIR="${LXD_DIR}" ns8="${ns}";;
    9)
      # shellcheck disable=SC2034
      LXD_NINE_DIR="${LXD_DIR}" ns9="${ns}";;
    *)
      echo "spawn_lxd_and_join_cluster: Unsupported index ${index}"
      false
      ;;
  esac

  echo "==> Spawn additional cluster node in ${ns} with storage driver ${driver}"

  LXD_NETNS="${ns}" spawn_lxd "${LXD_DIR}" false

  # If a custom cluster port was given, we need to first set the REST
  # API address.
  if [ "${port}" != "8443" ]; then
    lxc config set core.https_address "100.64.1.10${index}:8443"
  fi

  local preseed
  preseed="cluster:
  enabled: true
  server_name: node${index}
  server_address: 100.64.1.10${index}:${port}
  cluster_address: 100.64.1.10${target}:8443
  cluster_certificate: |
${cert}
  cluster_token: ${token}
  member_config:"

    # Declare the pool only if the driver is not ceph, because
    # the ceph pool doesn't need to be created on the joining
    # node (it's shared with the bootstrap one).
    if [ "${driver}" != "ceph" ]; then
    preseed+="
  - entity: storage-pool
    name: data
    key: source
    value: \"${source}\"
  - entity: storage-pool
    name: data
    key: source.recover
    value: ${source_recover}"

      if [ "${driver}" = "zfs" ]; then
      preseed+="
  - entity: storage-pool
    name: data
    key: zfs.pool_name
    value: lxdtest-$(basename "${TEST_DIR}")-${ns}"
      fi

      if [ "${driver}" = "lvm" ]; then
      preseed+="
  - entity: storage-pool
    name: data
    key: lvm.vg_name
    value: lxdtest-$(basename "${TEST_DIR}")-${ns}"
      fi

      # shellcheck disable=SC2235
      if [ "${source}" = "" ] && { [ "${driver}" = "btrfs" ] || [ "${driver}" = "zfs" ] || [ "${driver}" = "lvm" ]; }; then
      preseed+="
  - entity: storage-pool
    name: data
    key: size
    value: 1GiB"
      fi
    fi

    # Print the preseed for debugging purposes.
  echo "${preseed}"

  lxd init --preseed <<< "${preseed}"
}

respawn_lxd_cluster_member() {
  LXD_NETNS="${1}" respawn_lxd "${2}" true
}

is_uuid_v4() {
  # Case insensitive match for a v4 UUID. The third group must start with 4, and the fourth group must start with 8, 9,
  # a, or b. This accounts for the version and variant. See https://datatracker.ietf.org/doc/html/rfc9562#name-uuid-version-4.
  echo "${1}" | grep -ixE '[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}'
}

is_uuid_v7() {
  # Case insensitive match for a v7 UUID. The third group must start with 7, and the fourth group must start with 8, 9,
  # a, or b. This accounts for the version and variant. See https://datatracker.ietf.org/doc/html/rfc9562#name-uuid-version-7.
  echo "${1}" | grep -ixE '[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}'
}

# setup_replicator_volume_test wires two single-member clusters together as a leader
# (LXD_ONE_DIR) and standby (LXD_TWO_DIR) replicator pair over "replicator-project",
# then creates a custom volume pool named "volpool" that exists on both clusters. Custom
# volume replication requires the pool to exist with the same name on both sides, so the
# backend under test is exercised while the LXD pool name is kept identical. Sets the
# global LXD_ONE_DIR, LXD_TWO_DIR and vol_pool for the caller.
setup_replicator_volume_test() {
  # Create two standalone clustered LXD daemons to simulate two separate clusters.
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  spawn_lxd "${LXD_ONE_DIR}" true

  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  spawn_lxd "${LXD_TWO_DIR}" true

  # Enable clustering on both.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster enable node1
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster enable node2

  # Create projects on both clusters.
  LXD_DIR="${LXD_ONE_DIR}" lxc project create replicator-project
  LXD_DIR="${LXD_TWO_DIR}" lxc project create replicator-project

  # Setup auth groups and cluster links.
  local trust_token
  LXD_DIR="${LXD_ONE_DIR}" lxc auth group create replicator-group
  LXD_DIR="${LXD_ONE_DIR}" lxc auth group permission add replicator-group project replicator-project operator
  LXD_DIR="${LXD_ONE_DIR}" lxc auth group permission add replicator-group project replicator-project can_edit
  trust_token="$(LXD_DIR="${LXD_ONE_DIR}" lxc cluster link create lxd_two --quiet --auth-group replicator-group)"

  LXD_DIR="${LXD_TWO_DIR}" lxc auth group create replicator-group
  LXD_DIR="${LXD_TWO_DIR}" lxc auth group permission add replicator-group project replicator-project operator
  LXD_DIR="${LXD_TWO_DIR}" lxc auth group permission add replicator-group project replicator-project can_edit
  LXD_DIR="${LXD_TWO_DIR}" lxc cluster link create lxd_one --token "${trust_token}" --auth-group replicator-group

  # Configure replica project settings: standby sets replica.cluster, leader creates replicator.
  LXD_DIR="${LXD_TWO_DIR}" lxc project set replicator-project replica.cluster=lxd_one
  LXD_DIR="${LXD_ONE_DIR}" lxc replicator create my-replicator cluster=lxd_two --project replicator-project
  LXD_DIR="${LXD_TWO_DIR}" lxc project demote-replica replicator-project
  LXD_DIR="${LXD_ONE_DIR}" lxc project promote-replica replicator-project

  # Setup storage on both clusters.
  local pool_one pool_two poolDriver
  pool_one="lxdtest-$(basename "${LXD_ONE_DIR}")"
  pool_two="lxdtest-$(basename "${LXD_TWO_DIR}")"
  LXD_DIR="${LXD_ONE_DIR}" lxc profile device add default root disk path="/" pool="${pool_one}" --project replicator-project
  LXD_DIR="${LXD_TWO_DIR}" lxc profile device add default root disk path="/" pool="${pool_two}" --project replicator-project
  # Custom volumes must reside on a pool that exists with the same name on both clusters (pool-parity requirement).
  # Exercise the backend under test rather than always dir. For ceph the underlying pool is shared between the
  # two clusters, so keep the LXD pool name identical while giving each cluster a distinct ceph.osd.pool_name.
  poolDriver="$(storage_backend "${LXD_ONE_DIR}")"
  vol_pool="volpool"
  if [ "${poolDriver}" = "ceph" ]; then
    LXD_DIR="${LXD_ONE_DIR}" lxc storage create "${vol_pool}" ceph "ceph.osd.pool_name=lxdtest-$(basename "${LXD_ONE_DIR}")-${vol_pool}"
    LXD_DIR="${LXD_TWO_DIR}" lxc storage create "${vol_pool}" ceph "ceph.osd.pool_name=lxdtest-$(basename "${LXD_TWO_DIR}")-${vol_pool}"
  elif [ "${poolDriver}" = "lvm" ]; then
    LXD_DIR="${LXD_ONE_DIR}" lxc storage create "${vol_pool}" lvm "lvm.vg_name=lxdtest-$(basename "${LXD_ONE_DIR}")-${vol_pool}"
    LXD_DIR="${LXD_TWO_DIR}" lxc storage create "${vol_pool}" lvm "lvm.vg_name=lxdtest-$(basename "${LXD_TWO_DIR}")-${vol_pool}"
  elif [ "${poolDriver}" = "zfs" ]; then
    LXD_DIR="${LXD_ONE_DIR}" lxc storage create "${vol_pool}" zfs "zfs.pool_name=lxdtest-$(basename "${LXD_ONE_DIR}")-${vol_pool}"
    LXD_DIR="${LXD_TWO_DIR}" lxc storage create "${vol_pool}" zfs "zfs.pool_name=lxdtest-$(basename "${LXD_TWO_DIR}")-${vol_pool}"
  else
    LXD_DIR="${LXD_ONE_DIR}" lxc storage create "${vol_pool}" "${poolDriver}"
    LXD_DIR="${LXD_TWO_DIR}" lxc storage create "${vol_pool}" "${poolDriver}"
  fi
}

# teardown_replicator_volume_test removes the shared root disk device and the "volpool"
# custom volume pool from both clusters and shuts the daemons down. Callers must delete
# any instances and custom volumes they created first: the pool teardown only succeeds
# once the pool is empty.
teardown_replicator_volume_test() {
  LXD_DIR="${LXD_TWO_DIR}" lxc profile device remove default root --project replicator-project
  LXD_DIR="${LXD_ONE_DIR}" lxc profile device remove default root --project replicator-project
  LXD_DIR="${LXD_ONE_DIR}" lxc storage delete "${vol_pool}"
  LXD_DIR="${LXD_TWO_DIR}" lxc storage delete "${vol_pool}"
  kill_lxd "${LXD_TWO_DIR}"
  kill_lxd "${LXD_ONE_DIR}"
}
