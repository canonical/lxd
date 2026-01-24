test_syslog_socket() {
  lxc config set core.syslog_socket=true
  lxc monitor --type=ovn > "${TEST_DIR}/ovn.log" &
  monitorOVNPID=$!

  sleep 0.1
  echo "<29> ovs|ovn-controller|00017|rconn|INFO|unix:/var/run/openvswitch/br-int.mgmt: connected" | socat - unix-sendto:"${LXD_DIR}/syslog.socket"
  sleep 0.1

  kill_go_proc "${monitorOVNPID}"
  grep -qF "type: ovn" "${TEST_DIR}/ovn.log"
  grep -qF "unix:/var/run/openvswitch/br-int.mgmt: connected" "${TEST_DIR}/ovn.log"

  lxc config unset core.syslog_socket
  rm "${TEST_DIR}/ovn.log"
}
