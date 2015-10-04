test_fdleak() {
  LXD_FDLEAK_DIR=$(mktemp -d -p ${TEST_DIR} XXX)
  chmod +x ${LXD_FDLEAK_DIR}
  spawn_lxd ${LXD_FDLEAK_DIR}
  pid=$(cat ${LXD_FDLEAK_DIR}/lxd.pid)

  beforefds=`/bin/ls /proc/${pid}/fd | wc -l`
  (
    set -e
    LXD_DIR=$LXD_FDLEAK_DIR

    ensure_import_testimage

    for i in `seq 5`; do
      lxc init testimage leaktest${i}
      lxc info leaktest${i}
      lxc start leaktest${i}
      lxc exec leaktest${i} -- ps -ef
      lxc stop leaktest${i} --force
      lxc delete leaktest${i}
    done

    exit 0
  )
  afterfds=`/bin/ls /proc/${pid}/fd | wc -l`
  leakedfds=$((afterfds - beforefds))

  bad=0
  [ ${leakedfds} -gt 5 ] && bad=1 || true
  if [ ${bad} -eq 1 ]; then
    echo "${leakedfds} FDS leaked"
    ls /proc/${pid}/fd -al
    netstat -anp 2>&1 | grep ${pid}/
    false
  fi

  kill_lxd ${LXD_FDLEAK_DIR}
}
