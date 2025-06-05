test_lxd_benchmark_basic(){
  local count=5
  ensure_import_testimage
  lxd-benchmark init --count "${count}" testimage
  [ "$(lxc list -f csv -c n STATUS=stopped | grep -cwF benchmark)" = "${count}" ]
  lxd-benchmark start
  [ "$(lxc list -f csv -c n STATUS=stopped)" = "" ]
  [ "$(lxc list -f csv -c n STATUS=running | grep -cwF benchmark)" = "${count}" ]
  lxd-benchmark stop
  [ "$(lxc list -f csv -c n STATUS=running)" = "" ]
  [ "$(lxc list -f csv -c n STATUS=stopped | grep -cwF benchmark)" = "${count}" ]
  lxd-benchmark delete
  [ "$(lxc list -f csv -c n)" = "" ]
}

