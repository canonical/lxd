# Test helper for clustering

setup_clustering_bridge() {
  name="br$$"

  echo "==> Setup clustering bridge ${name}"

  brctl addbr "${name}"
  ip addr add 10.1.1.1/16 dev "${name}"
  ip link set dev "${name}" up

  iptables -t nat -A POSTROUTING -s 10.1.0.0/16 -d 0.0.0.0/0 -j MASQUERADE
  echo 1 > /proc/sys/net/ipv4/ip_forward
}

teardown_clustering_bridge() {
  name="br$$"

  if brctl show | grep -q "${name}" ; then
      echo "==> Teardown clustering bridge ${name}"
      echo 0 > /proc/sys/net/ipv4/ip_forward
      iptables -t nat -D POSTROUTING -s 10.1.0.0/16 -d 0.0.0.0/0 -j MASQUERADE
      ip link set dev "${name}" down
      ip addr del 10.1.1.1/16 dev "${name}"
      brctl delbr "${name}"
  fi
}

setup_clustering_netns() {
  id="${1}"
  shift

  prefix="lxd$$"
  ns="${prefix}${id}"
  rcfile="${TEST_DIR}/${ns}.conf"

  echo "==> Setup clustering netns ${ns}"

  cat > "${rcfile}" <<EOF
lxc.console.path=none
lxc.mount.entry = cgroup                 sys/fs/cgroup                  tmpfs   rw,nosuid,nodev,noexec,mode=755,create=dir                                   0 0
lxc.mount.entry = cgroup2                sys/fs/cgroup/unified          cgroup2 rw,nosuid,nodev,noexec,relatime,create=dir                                   0 0
lxc.mount.entry = name=systemd           sys/fs/cgroup/systemd          cgroup  rw,nosuid,nodev,noexec,relatime,xattr,clone_children,name=systemd,create=dir 0 0
lxc.mount.entry = net_cls,net_prio       sys/fs/cgroup/net_cls,net_prio cgroup  rw,nosuid,nodev,noexec,relatime,net_cls,net_prio,clone_children,create=dir   0 0
lxc.mount.entry = cpuset                 sys/fs/cgroup/cpuset           cgroup  rw,nosuid,nodev,noexec,relatime,cpuset,clone_children,create=dir             0 0
lxc.mount.entry = hugetlb                sys/fs/cgroup/hugetlb          cgroup  rw,nosuid,nodev,noexec,relatime,hugetlb,clone_children,create=dir            0 0
lxc.mount.entry = blkio                  sys/fs/cgroup/blkio            cgroup  rw,nosuid,nodev,noexec,relatime,blkio,clone_children,create=dir              0 0
lxc.mount.entry = cpu,cpuacct            sys/fs/cgroup/cpu,cpuacct      cgroup  rw,nosuid,nodev,noexec,relatime,cpu,cpuacct,clone_children,create=dir        0 0
lxc.mount.entry = pids                   sys/fs/cgroup/pids             cgroup  rw,nosuid,nodev,noexec,relatime,pids,clone_children,create=dir               0 0
lxc.mount.entry = rdma                   sys/fs/cgroup/rdma             cgroup  rw,nosuid,nodev,noexec,relatime,rdma,clone_children,create=dir               0 0
lxc.mount.entry = perf_event             sys/fs/cgroup/perf_event       cgroup  rw,nosuid,nodev,noexec,relatime,perf_event,clone_children,create=dir         0 0
lxc.mount.entry = memory                 sys/fs/cgroup/memory           cgroup  rw,nosuid,nodev,noexec,relatime,memory,clone_children,create=dir             0 0
lxc.mount.entry = freezer                sys/fs/cgroup/freezer          cgroup  rw,nosuid,nodev,noexec,relatime,freezer,clone_children,create=dir            0 0
lxc.mount.entry = /sys/fs/cgroup/devices sys/fs/cgroup/devices          none    bind,create=dir 0 0

# CGroup whitelist
lxc.cgroup.devices.deny = a
## Allow any mknod (but not reading/writing the node)
lxc.cgroup.devices.allow = c *:* m
lxc.cgroup.devices.allow = b *:* m
## Allow specific devices
### /dev/null
lxc.cgroup.devices.allow = c 1:3 rwm
### /dev/zero
lxc.cgroup.devices.allow = c 1:5 rwm
### /dev/full
lxc.cgroup.devices.allow = c 1:7 rwm
### /dev/tty
lxc.cgroup.devices.allow = c 5:0 rwm
### /dev/console
lxc.cgroup.devices.allow = c 5:1 rwm
### /dev/ptmx
lxc.cgroup.devices.allow = c 5:2 rwm
### /dev/random
lxc.cgroup.devices.allow = c 1:8 rwm
### /dev/urandom
lxc.cgroup.devices.allow = c 1:9 rwm
### /dev/pts/*
lxc.cgroup.devices.allow = c 136:* rwm
### fuse
lxc.cgroup.devices.allow = c 10:229 rwm
### loop
lxc.cgroup.devices.allow = b 7:* rwm

lxc.apparmor.profile = unconfined

lxc.pty.max = 1024
lxc.tty.max = 10
lxc.environment=TERM=xterm

lxc.hook.version = 1
lxc.hook.autodev = mknod /dev/loop-control c 10, 237
lxc.hook.autodev = mknod /dev/loop0 c 7 0
lxc.hook.autodev = mknod /dev/loop1 c 7 1
lxc.hook.autodev = mknod /dev/loop2 c 7 2
lxc.hook.autodev = mknod /dev/loop3 c 7 3
lxc.hook.autodev = mknod /dev/loop4 c 7 4
lxc.hook.autodev = mknod /dev/loop5 c 7 5
lxc.hook.autodev = mknod /dev/loop6 c 7 6
lxc.hook.autodev = mknod /dev/loop7 c 7 7
EOF
  lxc-execute -n "${ns}" --rcfile "${rcfile}" -- sh -c 'while true; do sleep 1; done' &
  sleep 1

  mkdir -p /run/netns
  touch "/run/netns/${ns}"

  pid="$(lxc-info -n "${ns}" -p | cut -f 2 -d : | tr -d " ")"
  mount --bind "/proc/${pid}/ns/net" "/run/netns/${ns}"

  veth1="v${ns}1"
  veth2="v${ns}2"

  ip link add "${veth1}" type veth peer name "${veth2}"
  ip link set "${veth2}" netns "${ns}"

  nsbridge="br$$"
  brctl addif "${nsbridge}" "${veth1}"

  ip link set "${veth1}" up
  (
    cat <<EOF
    ip link set dev lo up
    ip link set dev "${veth2}" name eth0
    ip link set eth0 up
    ip addr add "10.1.1.10${id}/16" dev eth0
    ip route add default via 10.1.1.1
EOF
  ) | nsenter --all --target="${pid}" sh
}

teardown_clustering_netns() {
  prefix="lxd$$"
  nsbridge="br$$"
  for ns in $(lxc-ls | grep "${prefix}") ; do
      echo "==> Teardown clustering netns ${ns}"
      pid="$(lxc-info -n "${ns}" -p | cut -f 2 -d : | tr -d " ")"
      veth1="v${ns}1"
      veth2="v${ns}2"
      nsenter --all --target="${pid}" ip link set eth0 down
      nsenter --all --target="${pid}" ip link set lo down
      ip link set "${veth1}" down
      brctl delif "${nsbridge}" "${veth1}"
      ip link delete "${veth1}" type veth
      umount "/run/netns/${ns}"
      rm "/run/netns/${ns}"
      lxc-stop -n "${ns}"
  done
}

spawn_lxd_and_bootstrap_cluster() {
  set -e
  ns="${1}"
  bridge="${2}"
  LXD_DIR="${3}"
  LXD_NETNS="${ns}" spawn_lxd "${LXD_DIR}" false
  (
    set -e

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
  name: node1
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

  LXD_NETNS="${ns}" spawn_lxd "${LXD_DIR}" false
  (
    set -e

    cat <<EOF | lxd init --preseed
config:
  core.https_address: 10.1.1.10${index}:8443
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
  name: node${index}
  target_address: 10.1.1.10${target}:8443
  target_password: sekret
  target_cert: "$cert"
EOF
  )
}
