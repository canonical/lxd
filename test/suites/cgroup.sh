#
test_snap_cgroup() {
  # The snap is required due to relying on lxcfs features

  if uname -a | grep -F -- -kvm; then
    export TEST_UNMET_REQUIREMENT="The -kvm kernel flavor does not have the required extra modules to test limits.egress/limits.ingress"
    return
  fi

  # Setup swap if none exist
  setup_swap

  # To run network limit tests
  install_tools iperf3

  LXCFS_SUPPORTS_SWAP_ACCOUNTING=0
  if journalctl --quiet --no-hostname --no-pager --boot=0 --unit=snap.lxd.daemon.service --grep "Kernel supports swap accounting"; then
      LXCFS_SUPPORTS_SWAP_ACCOUNTING=1
  fi

  echo "==> Setup network with DHCP for iperf3 tests"
  lxc network create lxdt$$ ipv4.address=192.0.2.1/24 ipv6.address=none ipv4.nat=true

  echo "==> Start a container with no limits"
  # The usual testimage does not have iperf3 installed so use a full fledged image instead
  lxc launch ubuntu-minimal-daily:24.04 c1 -n lxdt$$

  echo "==> Validate default values"
  [ "$(lxc exec c1 -- nproc)" = "$(nproc)" ]
  [ "$(lxc exec c1 -- grep ^MemTotal /proc/meminfo)" = "$(grep ^MemTotal /proc/meminfo)" ]
  if [ -e "/sys/fs/cgroup/memory/memory.memsw.limit_in_bytes" ] ||
     { [ -e "/sys/fs/cgroup/system.slice/memory.swap.max" ] && [ "${LXCFS_SUPPORTS_SWAP_ACCOUNTING}" -eq 1 ]; }; then
      [ "$(lxc exec c1 -- grep ^SwapTotal /proc/meminfo)" = "$(grep ^SwapTotal /proc/meminfo)" ]
  else
      [ "$(lxc exec c1 -- grep ^SwapTotal /proc/meminfo)" = "SwapTotal:             0 kB" ]
  fi

  if [ -e "/sys/fs/cgroup/cpu" ]; then
      [ "$(lxc exec c1 -- cat /sys/fs/cgroup/cpu/cpu.shares)" = "1024" ]
  else
      [ "$(lxc exec c1 -- cat /sys/fs/cgroup/cpu.weight)" = "100" ]
  fi

  if [ -e "/sys/fs/cgroup/pids" ]; then
      [ "$(lxc exec c1 -- cat /sys/fs/cgroup/pids/pids.max)" = "max" ]
  else
      [ "$(lxc exec c1 -- cat /sys/fs/cgroup/pids.max)" = "max" ]
  fi

  if [ -e "/sys/fs/cgroup/memory" ]; then
      [ "$(lxc exec c1 -- cat /sys/fs/cgroup/memory/memory.swappiness)" = "$(cat /sys/fs/cgroup/memory/memory.swappiness)" ]
  fi

  if [ -e "/sys/fs/cgroup/net_prio" ]; then
      [ "$(lxc exec c1 -- cat /sys/fs/cgroup/net_prio/net_prio.ifpriomap)" = "$(cat /sys/fs/cgroup/net_prio/net_prio.ifpriomap)" ]
  fi

  NPROC="$(nproc)"
  if [ "${NPROC}" -lt 3 ]; then
      # The number 3 comes from `limits.cpu=0,2` which takes the 1st and 3rd host's CPUs
      echo "This test requires 3 CPUs or more to be available (not ${NPROC})" >&2
      exit 1
  fi

  echo "==> Testing CPU limits"
  lxc config set c1 limits.cpu=2
  [ "$(lxc exec c1 -- nproc)" = "2" ]

  # trying to allocate more CPUs than there are available should silently be capped at NPROC
  lxc config set c1 limits.cpu="$((NPROC+2))"
  [ "$(lxc exec c1 -- nproc)" = "${NPROC}" ]

  lxc config set c1 limits.cpu=0,2
  [ "$(lxc exec c1 -- nproc)" = "2" ]

  lxc config set c1 limits.cpu=2-2
  [ "$(lxc exec c1 -- nproc)" = "1" ]

  lxc config unset c1 limits.cpu
  # XXX: avoid a race (https://github.com/canonical/lxd/issues/12659)
  sleep 2
  [ "$(lxc exec c1 -- nproc)" = "${NPROC}" ]

  lxc config set c1 limits.cpu.priority 5
  if [ -e "/sys/fs/cgroup/cpu" ]; then
      [ "$(lxc exec c1 -- cat /sys/fs/cgroup/cpu/cpu.shares)" = "1019" ]
  else
      [ "$(lxc exec c1 -- cat /sys/fs/cgroup/cpu.weight)" = "95" ]
  fi

  lxc config unset c1 limits.cpu.priority
  if [ -e "/sys/fs/cgroup/cpu" ]; then
      [ "$(lxc exec c1 -- cat /sys/fs/cgroup/cpu/cpu.shares)" = "1024" ]
  else
      [ "$(lxc exec c1 -- cat /sys/fs/cgroup/cpu.weight)" = "100" ]
  fi

  lxc config set c1 limits.cpu.allowance 10%
  if [ -e "/sys/fs/cgroup/cpu" ]; then
      [ "$(lxc exec c1 -- cat /sys/fs/cgroup/cpu/cpu.shares)" = "102" ]
  else
      [ "$(lxc exec c1 -- cat /sys/fs/cgroup/cpu.weight)" = "10" ]
  fi

  lxc config set c1 limits.cpu.allowance 10ms/100ms
  if [ -e "/sys/fs/cgroup/cpu" ]; then
      [ "$(lxc exec c1 -- cat /sys/fs/cgroup/cpu/cpu.shares)" = "1024" ]
      [ "$(lxc exec c1 -- cat /sys/fs/cgroup/cpu/cpu.cfs_period_us)" = "100000" ]
      [ "$(lxc exec c1 -- cat /sys/fs/cgroup/cpu/cpu.cfs_quota_us)" = "10000" ]
  else
      [ "$(lxc exec c1 -- cat /sys/fs/cgroup/cpu.weight)" = "100" ]
      [ "$(lxc exec c1 -- cat /sys/fs/cgroup/cpu.max)" = "10000 100000" ]
  fi
  lxc config unset c1 limits.cpu.allowance

  echo "==> Testing memory limits"
  MEM_LIMIT_MIB=512
  lxc config set c1 limits.memory="${MEM_LIMIT_MIB}MiB"
  [ "$(lxc exec c1 -- grep ^MemTotal /proc/meminfo)" = "MemTotal:         $((MEM_LIMIT_MIB * 1024)) kB" ]
  if [ -e "/sys/fs/cgroup/memory/memory.memsw.limit_in_bytes" ] ||
     { [ -e "/sys/fs/cgroup/system.slice/memory.swap.max" ] && [ "${LXCFS_SUPPORTS_SWAP_ACCOUNTING}" -eq 1 ]; }; then
      [ "$(lxc exec c1 -- grep ^SwapTotal /proc/meminfo)" = "SwapTotal:        $((MEM_LIMIT_MIB * 1024)) kB" ]
  else
      [ "$(lxc exec c1 -- grep ^SwapTotal /proc/meminfo)" = "SwapTotal:             0 kB" ]
  fi

  # ensure that we don't set memory.high when limits.memory.enforce=hard (default value)
  if [ -e "/sys/fs/cgroup/system.slice/memory.high" ]; then
      [ "$(lxc exec c1 -- cat /sys/fs/cgroup/memory.max)" = $((MEM_LIMIT_MIB * 1024 * 1024)) ]
      [ "$(lxc exec c1 -- cat /sys/fs/cgroup/memory.high)" = "max" ]
  fi

  if [ -e "/sys/fs/cgroup/memory" ]; then
      lxc config set c1 limits.memory.swap=false
      [ "$(lxc exec c1 -- cat /sys/fs/cgroup/memory/memory.swappiness)" = "0" ]
      [ "$(lxc exec c1 -- grep ^SwapTotal /proc/meminfo)" = "SwapTotal:             0 kB" ]

      lxc config set c1 limits.memory.swap=true
      lxc config set c1 limits.memory.swap.priority=5
      # swappiness is 70 - 5 (priority)
      [ "$(lxc exec c1 -- cat /sys/fs/cgroup/memory/memory.swappiness)" = "65" ]
  fi
  if [ -e "/sys/fs/cgroup/memory/memory.memsw.limit_in_bytes" ] ||
     { [ -e "/sys/fs/cgroup/system.slice/memory.swap.max" ] && [ "${LXCFS_SUPPORTS_SWAP_ACCOUNTING}" -eq 1 ]; }; then
      lxc config set c1 limits.memory 128MiB
      [ "$(lxc exec c1 -- grep ^MemTotal /proc/meminfo)" = "MemTotal:         131072 kB" ]

      lxc exec c1 -- mkdir -p /root/dd
      lxc exec c1 -- mount -t tmpfs tmpfs /root/dd -o size=2G
      lxc exec c1 -- dd if=/dev/zero of=/root/dd/blob bs=4M count=16
      dmesg -c >/dev/null 2>&1
      # shellcheck disable=SC2251
      ! lxc exec c1 -- dd if=/dev/zero of=/root/dd/blob bs=4M count=64
      dmesg | grep -F "Memory cgroup out of memory: Killed process"
      dmesg -c >/dev/null 2>&1

      # shellcheck disable=SC2251
      ! lxc stop c1 --force

      # Wait for post-stop to complete
      sleep 2s

      lxc config set c1 limits.memory.enforce soft
      lxc start c1
      lxc exec c1 -- mkdir -p /root/dd
      lxc exec c1 -- mount -t tmpfs tmpfs /root/dd -o size=2G
      dmesg -c >/dev/null 2>&1
      lxc exec c1 -- dd if=/dev/zero of=/root/dd/blob bs=4M count=64
      dmesg | grep -F "Memory cgroup out of memory: Killed process" && false
      dmesg -c >/dev/null 2>&1

      # shellcheck disable=SC2251
      ! lxc stop c1 --force

      # Wait for post-stop to complete
      sleep 2s

      lxc config set c1 limits.memory= limits.memory.enforce=
      lxc start c1
  fi

  echo "==> Testing process limits"
  lxc config set c1 limits.processes=2000
  if [ -e "/sys/fs/cgroup/pids" ]; then
      [ "$(lxc exec c1 -- cat /sys/fs/cgroup/pids/pids.max)" = "2000" ]
  else
      [ "$(lxc exec c1 -- cat /sys/fs/cgroup/pids.max)" = "2000" ]
  fi

  lxc restart c1 --force
  waitInstanceReady c1
  IP="192.0.2.1"
  lxc exec c1 -- apt-get update
  lxc exec c1 --env DEBIAN_FRONTEND=noninteractive -- apt-get install --no-install-recommends --yes iperf3

  iperf3 -s -D
  lxc exec c1 -- iperf3 -t 3 -c "${IP}" -J > iperf.json
  E1=$(($(jq .end.sum_sent.bits_per_second < iperf.json | cut -d. -f1)/1024/1024))
  lxc exec c1 -- iperf3 -t 3 -R -c "${IP}" -J > iperf.json
  I1=$(($(jq .end.sum_sent.bits_per_second < iperf.json | cut -d. -f1)/1024/1024))
  echo "Throughput before limits: ${E1}Mbps / ${I1}Mbps"

  lxc config device set c1 eth0 limits.egress=50Mbit limits.ingress=200Mbit
  lxc exec c1 -- iperf3 -t 3 -c "${IP}" -J > iperf.json
  E2=$(($(jq .end.sum_sent.bits_per_second < iperf.json | cut -d. -f1)/1024/1024))
  lxc exec c1 -- iperf3 -t 3 -R -c "${IP}" -J > iperf.json
  I2=$(($(jq .end.sum_sent.bits_per_second < iperf.json | cut -d. -f1)/1024/1024))
  echo "Throughput after limits: ${E2}Mbps / ${I2}Mbps"
  [ "${E2}" -lt "50" ]
  [ "${I2}" -lt "200" ]

  pkill -9 iperf3
  lxc config device set c1 eth0 limits.egress= limits.ingress=

  echo "==> Testing disk limits"
  if [ -e /sys/fs/cgroup/init.scope/io.weight ]; then
      lxc config set c1 limits.disk.priority=5
      [ "$(lxc exec c1 -- cat /sys/fs/cgroup/io.weight)" = "default 500" ]
  elif [ -e /sys/fs/cgroup/blkio/blkio.weight ]; then
      lxc config set c1 limits.disk.priority=5
      [ "$(lxc exec c1 -- cat /sys/fs/cgroup/blkio/blkio.weight)" = "500" ]
  fi

  lxc config device override c1 root limits.read=10iops limits.write=20iops
  if [ -e /sys/fs/cgroup/init.scope/io.max ]; then
      lxc exec c1 -- grep "riops=10 wiops=20" /sys/fs/cgroup/io.max
  else
      lxc exec c1 -- grep "10$" /sys/fs/cgroup/blkio/blkio.throttle.read_iops_device
      lxc exec c1 -- grep "20$" /sys/fs/cgroup/blkio/blkio.throttle.write_iops_device
  fi

  lxc config device set c1 root limits.read=10MB limits.write=20MB
  if [ -e /sys/fs/cgroup/init.scope/io.max ]; then
      lxc exec c1 -- grep "rbps=10000000 wbps=20000000" /sys/fs/cgroup/io.max
  else
      lxc exec c1 -- grep "10000000$" /sys/fs/cgroup/blkio/blkio.throttle.read_bps_device
      lxc exec c1 -- grep "20000000$" /sys/fs/cgroup/blkio/blkio.throttle.write_bps_device
  fi

  lxc config device set c1 root limits.read= limits.write=

  echo "==> Testing the freezer"
  lxc pause c1
  ! lxc exec c1 bash || false
  lxc start c1

  echo "==> Cleaning up"
  lxc delete -f c1
  lxc network delete lxdt$$
  teardown_swap
}
