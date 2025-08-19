test_lxd_benchmark_basic(){
  local count=5
  local report_file
  report_file="$(mktemp -p "${TEST_DIR}" XXX)"

  # lxd-benchmark should fail if the provided image doesn't exist
  ! lxd-benchmark launch --count 1 local:does-not-exist 2>/dev/null || false

  ensure_import_testimage

  # Initial smoke test.
  lxd-benchmark launch --count 1 --report-file "${report_file}" testimage
  lxd-benchmark launch --freeze --privileged --count 1 --report-file "${report_file}" testimage
  lxd-benchmark start --report-file "${report_file}"
  lxd-benchmark delete --report-file "${report_file}"

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
  [ "$(wc -l < "${report_file}")" = "9" ]

  # cleanup
  rm "${report_file}"
}

