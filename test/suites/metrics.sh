test_metrics() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  lxc config set core.https_address "${LXD_ADDR}"

  lxc launch testimage c1
  lxc init testimage c2

  # c1 metrics should show as the container is running
  lxc query "/1.0/metrics" | grep "name=\"c1\""

  # c2 metrics should show the container as stopped
  lxc query "/1.0/metrics" | grep "name=\"c2\""
  lxc query "/1.0/metrics" | grep "name=\"c2\"" | grep "state=\"STOPPED\""

  # create new certificate
  openssl req -x509 -newkey rsa:2048 -keyout "${TEST_DIR}/metrics.key" -nodes -out "${TEST_DIR}/metrics.crt" -subj "/CN=lxd.local"

  # this should fail as the certificate is not trusted yet
  curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${LXD_ADDR}/1.0/metrics" | grep "\"error_code\":403"

  # trust newly created certificate for metrics only
  lxc config trust add "${TEST_DIR}/metrics.crt" --type=metrics

  # c1 metrics should show as the container is running
  curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${LXD_ADDR}/1.0/metrics" | grep "name=\"c1\""

  # c2 metrics should show the container as stopped
  curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${LXD_ADDR}/1.0/metrics" | grep "name=\"c2\"" | grep "state=\"STOPPED\""

  # make sure nothing else can be done with this certificate
  curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${LXD_ADDR}/1.0/instances" | grep "\"error_code\":403"

  metrics_addr="127.0.0.1:$(local_tcp_port)"

  lxc config set core.metrics_address "${metrics_addr}"

  # c1 metrics should show as the container is running
  curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${metrics_addr}/1.0/metrics" | grep "name=\"c1\""

  # c2 metrics should show the container as stopped
  curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${metrics_addr}/1.0/metrics" | grep "name=\"c2\"" | grep "state=\"STOPPED\""

  # make sure no other endpoint is available
  curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${metrics_addr}/1.0/instances" | grep "\"error_code\":404"

  # test unauthenticated connections
  ! curl -k -s -X GET "https://${metrics_addr}/1.0/metrics" | grep "name=\"c1\"" || false
  lxc config set core.metrics_authentication=false
  curl -k -s -X GET "https://${metrics_addr}/1.0/metrics" | grep "name=\"c1\""

  lxc delete -f c1 c2
}
