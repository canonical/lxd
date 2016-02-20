#!/bin/sh

test_cpu_profiling() {
  LXD3_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD3_DIR}"
  spawn_lxd "${LXD3_DIR}" --cpuprofile "${LXD3_DIR}/cpu.out"
  lxdpid=$(cat "${LXD3_DIR}/lxd.pid")
  kill -TERM "${lxdpid}"
  wait "${lxdpid}" || true
  export PPROF_TMPDIR="${TEST_DIR}/pprof"
  echo top5 | go tool pprof "$(which lxd)" "${LXD3_DIR}/cpu.out"
  echo ""

  kill_lxd "${LXD3_DIR}"
}

test_mem_profiling() {
  LXD4_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD4_DIR}"
  spawn_lxd "${LXD4_DIR}" --memprofile "${LXD4_DIR}/mem"
  lxdpid=$(cat "${LXD4_DIR}/lxd.pid")

  if [ -e "${LXD4_DIR}/mem" ]; then
    false
  fi

  kill -USR1 "${lxdpid}"

  timeout=50
  while [ "${timeout}" != "0" ]; do
    [ -e "${LXD4_DIR}/mem" ] && break
    sleep 0.1
    timeout=$((timeout-1))
  done

  export PPROF_TMPDIR="${TEST_DIR}/pprof"
  echo top5 | go tool pprof "$(which lxd)" "${LXD4_DIR}/mem"
  echo ""

  kill_lxd "${LXD4_DIR}"
}
