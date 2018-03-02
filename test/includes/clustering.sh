# Test helper for clustering

setup_clustering_bridge() {
  name="br$$"

  echo "==> Setup clustering bridge ${name}"

  ip link add "${name}" up type bridge
  ip addr add 10.1.1.1/16 dev "${name}"

  # shellcheck disable=SC2039
  for i in {1..5}; do
      # Retry a few times since the xtables lock might be hold.
      if iptables -t nat -A POSTROUTING -s 10.1.0.0/16 -d 0.0.0.0/0 -j MASQUERADE; then
        break
      fi
      if [ "$i" -eq 5 ]; then
        return 1
      fi
      sleep 0.1
  done
  echo 1 > /proc/sys/net/ipv4/ip_forward
}

teardown_clustering_bridge() {
  name="br$$"

  if [ -e "/sys/class/net/${name}" ]; then
      echo "==> Teardown clustering bridge ${name}"
      echo 0 > /proc/sys/net/ipv4/ip_forward
      # shellcheck disable=SC2039
      for i in {1..5}; do
          # Retry a few times since the xtables lock might be hold.
          if iptables -t nat -A POSTROUTING -s 10.1.0.0/16 -d 0.0.0.0/0 -j MASQUERADE; then
              break
          fi
          if [ "$i" -eq 5 ]; then
              return 1
          fi
          sleep 0.1
      done
      ip link del dev "${name}"
  fi
}

setup_clustering_netns() {
  id="${1}"
  shift

  prefix="lxd$$"
  ns="${prefix}${id}"

  echo "==> Setup clustering netns ${ns}"

  (
    cat << EOF
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
sleep 300&
echo \$! > "${TEST_DIR}/ns/${ns}/PID"
EOF
  ) | unshare -m -n /bin/sh

  veth1="v${ns}1"
  veth2="v${ns}2"
  nspid=$(cat "${TEST_DIR}/ns/${ns}/PID")

  ip link add "${veth1}" type veth peer name "${veth2}"
  ip link set "${veth2}" netns "${nspid}"

  nsbridge="br$$"
  ip link set dev "${veth1}" master "${nsbridge}" up
  (
    cat <<EOF
set -e

ip link set dev lo up
ip link set dev "${veth2}" name eth0
ip link set eth0 up
ip addr add "10.1.1.10${id}/16" dev eth0
ip route add default via 10.1.1.1
EOF
  ) | nsenter -n -m -t "${nspid}" /bin/sh
}

teardown_clustering_netns() {
  prefix="lxd$$"
  nsbridge="br$$"

  [ ! -d "${TEST_DIR}/ns/" ] && return

  # shellcheck disable=SC2045
  for ns in $(ls -1 "${TEST_DIR}/ns/"); do
      echo "==> Teardown clustering netns ${ns}"

      veth1="v${ns}1"
      ip link del "${veth1}"

      pid="$(cat "${TEST_DIR}/ns/${ns}/PID")"
      kill -9 "${pid}"

      umount -l "${TEST_DIR}/ns/${ns}/net" >/dev/null 2>&1 || true
      rm -Rf "${TEST_DIR}/ns/${ns}"
  done
}

spawn_lxd_and_bootstrap_cluster() {
  set -e
  ns="${1}"
  bridge="${2}"
  LXD_DIR="${3}"
  LXD_NETNS="${ns}" spawn_lxd "${LXD_DIR}" false
  sleep 1
  (
    set -e

    cat <<EOF | lxd init --preseed
config:
  core.trust_password: sekret
  core.https_address: 10.1.1.101:8443
  images.auto_update_interval: 0
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
  server_name: node1
  enabled: true
EOF
  )
}

spawn_lxd_and_join_cluster() {
  set -e
  ns="${1}"
  bridge="${2}"
  cert="${3}"
  index="${4}"
  target="${5}"
  LXD_DIR="${6}"

  LXD_ALT_CERT=1 LXD_NETNS="${ns}" spawn_lxd "${LXD_DIR}" false
  sleep 1
  (
    set -e

    cat <<EOF | lxd init --preseed
config:
  core.https_address: 10.1.1.10${index}:8443
  images.auto_update_interval: 0
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
  server_name: node${index}
  enabled: true
  cluster_address: 10.1.1.10${target}:8443
  cluster_certificate: "$cert"
cluster_password: sekret
EOF
  )
}
