test_lxd_benchmark_basic(){
  local count=5
  local report_file
  report_file="$(mktemp -p "${TEST_DIR}" XXX)"

  ensure_import_testimage

  lxd-benchmark init --count "${count}" --report-file "${report_file}" testimage
  [ "$(lxc list -f csv -c n STATUS=stopped | grep -cwF benchmark)" = "${count}" ]
  lxd-benchmark start --report-file "${report_file}"
  [ "$(lxc list -f csv -c n STATUS=stopped)" = "" ]
  [ "$(lxc list -f csv -c n STATUS=running | grep -cwF benchmark)" = "${count}" ]
  lxd-benchmark stop --report-file "${report_file}"
  [ "$(lxc list -f csv -c n STATUS=running)" = "" ]
  [ "$(lxc list -f csv -c n STATUS=stopped | grep -cwF benchmark)" = "${count}" ]
  lxd-benchmark delete --report-file "${report_file}"
  [ "$(lxc list -f csv -c n)" = "" ]

  # Check the number of lines matches the number of commands + the header line
  cat "${report_file}"
  [ "$(wc -l < "${report_file}")" = "5" ]

  # cleanup
  rm "${report_file}"
}

