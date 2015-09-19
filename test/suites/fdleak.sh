test_fdleak() {
  ensure_import_testimage

  lxd1_pid=`ps -ef | grep lxd | grep -v grep | awk '/127.0.0.1:18443/ { print $2 }'`
  echo "lxd1_pid is $lxd1_pid"
  beforefds=`/bin/ls /proc/$lxd1_pid/fd | wc -l`
  for i in `seq 5`; do
    lxc init testimage leaktest1
    lxc info leaktest1
    if [ -z "${TRAVIS_PULL_REQUEST:-}" ]; then
      lxc start leaktest1
      lxc exec leaktest1 -- ps -ef
      lxc stop leaktest1 --force
    fi
    lxc delete leaktest1
  done

  afterfds=`/bin/ls /proc/$lxd1_pid/fd | wc -l`
  leakedfds=$((afterfds - beforefds))

  bad=0
  [ $leakedfds -gt 5 ] && bad=1 || true
  if [ $bad -eq 1 ]; then
    echo "$leakedfds FDS leaked"
    ls /proc/$lxd1_pid/fd -al
    false
  fi
}
