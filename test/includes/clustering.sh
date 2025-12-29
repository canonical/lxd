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
  shift

  local prefix="lxd$$"
  local ns="${prefix}${id}"

  echo "==> Setup clustering netns ${ns}"

  cat << EOF | unshare -m -n /bin/sh
set -e
mkdir -p "${TEST_DIR}/ns/${ns}"
touch "${TEST_DIR}/ns/${ns}/net"
mount -o bind /proc/self/ns/net "${TEST_DIR}/ns/${ns}/net"
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
cat << EOE > /usr/local/bin/in-hostnetns
#!/bin/sh
exec ip netns exec hostns /usr/bin/\\\$(basename \\\$0) "\\\$@"
EOE
chmod +x /usr/local/bin/in-hostnetns
# Setup ceph
ln -s in-hostnetns /usr/local/bin/ceph
ln -s in-hostnetns /usr/local/bin/rbd

sleep 300&
echo \$! > "${TEST_DIR}/ns/${ns}/PID"
EOF

  local veth1="v${ns}1"
  local veth2="v${ns}2"
  local nspid
  nspid=$(< "${TEST_DIR}/ns/${ns}/PID")

  ip link add "${veth1}" type veth peer name "${veth2}"
  ip link set "${veth2}" netns "${nspid}"

  local nsbridge="br$$"
  ip link set dev "${veth1}" master "${nsbridge}" up
  cat << EOF | nsenter -n -m -t "${nspid}" /bin/sh
set -e

ip link set dev lo up
ip link set dev "${veth2}" name eth0
ip link set eth0 up
ip addr add "100.64.1.10${id}/16" dev eth0
ip route add default via 100.64.1.1
ip link add localBridge${id} type bridge
EOF
}

teardown_clustering_netns() {
  local ns veth1

  [ -d "${TEST_DIR}/ns/" ] || return

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
  done
}

spawn_lxd_and_bootstrap_cluster() {
  ns="${1}"
  bridge="${2}"
  LXD_DIR="${3}"
  local driver="${4:-dir}"
  local port="${5:-}"

  echo "==> Spawn bootstrap cluster node in ${ns} with storage driver ${driver}"

  LXD_NETNS="${ns}" spawn_lxd "${LXD_DIR}" false

  local preseed
  preseed="config:
  core.https_address: 100.64.1.101:8443"

    if [ "${port}" != "" ]; then
    preseed+="
  cluster.https_address: 100.64.1.101:${port}"
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
    source: lxdtest-$(basename "${TEST_DIR}")
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
  server_name: node1
  enabled: true"

  # Print the preseed for debugging purposes.
  echo "${preseed}"

  lxd init --preseed <<< "${preseed}"
}

spawn_lxd_and_join_cluster() {
  ns="${1}"
  bridge="${2}"
  cert="${3}"
  index="${4}"
  target="${5}"
  LXD_DIR="${6}"
  local token="${7}"
  if [ -d "${7}" ]; then
    token="$(LXD_DIR=${7} lxc cluster add --quiet "node${index}")"
  fi
  local driver="${8:-dir}"
  local port="${9:-8443}"
  local source="${10:-}"
  local source_recover="${11:-false}"

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
  cluster_certificate: \"${cert}\"
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
