test_metrics() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  lxc config set core.https_address "${LXD_ADDR}"

  lxc launch testimage c1
  lxc init testimage c2

  # create another container in the non default project
  lxc project create foo -c features.images=false -c features.profiles=false
  lxc launch testimage c3 --project foo

  # c1 metrics should show as the container is running
  lxc query "/1.0/metrics" | grep "name=\"c1\""
  lxc query "/1.0/metrics?project=default" | grep "name=\"c1\""

  # c2 metrics should not be shown as the container is stopped
  ! lxc query "/1.0/metrics" | grep "name=\"c2\"" || false
  ! lxc query "/1.0/metrics?project=default" | grep "name=\"c2\"" || false

  # Check that we can get the count of existing instances.
  lxc query /1.0/metrics | grep -xF 'lxd_instances{project="default",type="container"} 2'
  lxc query /1.0/metrics | grep -xF 'lxd_instances{project="foo",type="container"} 1'
  # Ensure lxd_instances reports VM count properly (0)
  lxc query /1.0/metrics | grep -xF 'lxd_instances{project="default",type="virtual-machine"} 0'
  lxc query /1.0/metrics | grep -xF 'lxd_instances{project="foo",type="virtual-machine"} 0'

  # c3 metrics from another project also show up for non metrics unrestricted certificate
  lxc query "/1.0/metrics" | grep "name=\"c3\""
  lxc query "/1.0/metrics?project=foo" | grep "name=\"c3\""

  # create new certificate
  gen_cert_and_key "${TEST_DIR}/metrics.key" "${TEST_DIR}/metrics.crt" "metrics.local"

  # this should fail as the certificate is not trusted yet
  curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${LXD_ADDR}/1.0/metrics" | grep "\"error_code\":403"

  # trust newly created certificate for metrics only
  lxc config trust add "${TEST_DIR}/metrics.crt" --type=metrics
  lxc config trust show "$(openssl x509 -in "${TEST_DIR}/metrics.crt" -outform der | sha256sum | head -c12)" | grep -xF "restricted: false"

  # c1 metrics should show as the container is running
  curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${LXD_ADDR}/1.0/metrics" | grep "name=\"c1\""
  curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${LXD_ADDR}/1.0/metrics?project=default" | grep "name=\"c1\""

  # c2 metrics should not be shown as the container is stopped
  ! curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${LXD_ADDR}/1.0/metrics" | grep "name=\"c2\"" || false
  ! curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${LXD_ADDR}/1.0/metrics?project=default" | grep "name=\"c2\"" || false

  # c3 metrics from another project should be shown for unrestricted certificate
  curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${LXD_ADDR}/1.0/metrics" | grep "name=\"c3\""
  curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${LXD_ADDR}/1.0/metrics?project=foo" | grep "name=\"c3\""

  # internal server metrics should be shown as the certificate is not restricted
  curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${LXD_ADDR}/1.0/metrics" | grep -E "^lxd_warnings_total [0-9]+$"
  curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${LXD_ADDR}/1.0/metrics?project=default" | grep -E "^lxd_warnings_total [0-9]+$"

  # make sure nothing else can be done with this certificate
  curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${LXD_ADDR}/1.0/instances" | grep "\"error_code\":403"

  metrics_addr="127.0.0.1:$(local_tcp_port)"

  lxc config set core.metrics_address "${metrics_addr}"

  # c1 metrics should be shown as the container is running
  curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${metrics_addr}/1.0/metrics" | grep "name=\"c1\""
  curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${metrics_addr}/1.0/metrics?project=default" | grep "name=\"c1\""

  # c2 metrics should not be shown as the container is stopped
  ! curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${metrics_addr}/1.0/metrics" | grep "name=\"c2\"" || false
  ! curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${metrics_addr}/1.0/metrics?project=default" | grep "name=\"c2\"" || false

  # c3 metrics from another project should  be shown for unrestricted metrics certificate
  curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${LXD_ADDR}/1.0/metrics" | grep "name=\"c3\""
  curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${LXD_ADDR}/1.0/metrics?project=foo" | grep "name=\"c3\""

  # internal server metrics should be shown as the certificate is not restricted
  curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${metrics_addr}/1.0/metrics" | grep -E "^lxd_warnings_total [0-9]+$"
  curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${metrics_addr}/1.0/metrics?project=default" | grep -E "^lxd_warnings_total [0-9]+$"

  # make sure no other endpoint is available
  curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${metrics_addr}/1.0/instances" | grep "\"error_code\":404"

  # create new certificate
  gen_cert_and_key "${TEST_DIR}/metrics-restricted.key" "${TEST_DIR}/metrics-restricted.crt" "metrics-restricted.local"

  # trust newly created certificate for metrics only and mark it as restricted for the foo project
  lxc config trust add "${TEST_DIR}/metrics-restricted.crt" --type=metrics --restricted --projects foo
  lxc config trust show "$(openssl x509 -in "${TEST_DIR}/metrics-restricted.crt" -outform der | sha256sum | head -c12)" | grep -xF "restricted: true"

  # c3 metrics should be showned
  curl -k -s --cert "${TEST_DIR}/metrics-restricted.crt" --key "${TEST_DIR}/metrics-restricted.key" -X GET "https://${LXD_ADDR}/1.0/metrics?project=foo" | grep "name=\"c3\""

  # c3 metrics cannot be viewed via the generic metrics endpoint if the certificate is restricted
  ! curl -k -s --cert "${TEST_DIR}/metrics-restricted.crt" --key "${TEST_DIR}/metrics-restricted.key" -X GET "https://${LXD_ADDR}/1.0/metrics"

  # other projects metrics aren't visible as they aren't allowed for the restricted certificate
  ! curl -k -s --cert "${TEST_DIR}/metrics-restricted.crt" --key "${TEST_DIR}/metrics-restricted.key" -X GET "https://${LXD_ADDR}/1.0/metrics?project=default"

  # c1 and c2 metrics are not visible as they are in another project
  ! curl -k -s --cert "${TEST_DIR}/metrics-restricted.crt" --key "${TEST_DIR}/metrics-restricted.key" -X GET "https://${metrics_addr}/1.0/metrics?project=foo" | grep "name=\"c1\""
  ! curl -k -s --cert "${TEST_DIR}/metrics-restricted.crt" --key "${TEST_DIR}/metrics-restricted.key" -X GET "https://${metrics_addr}/1.0/metrics?project=foo" | grep "name=\"c2\""

  # Check that we can get the count of existing containers. There should be two in the default project: c1 (RUNNING) and c2 (STOPPED).
  curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${metrics_addr}/1.0/metrics" | grep -xF 'lxd_instances{project="default",type="container"} 2'
  sleep 10
  # Try again after the metric cache has expired. We should still see two containers.
  curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${metrics_addr}/1.0/metrics" | grep -xF 'lxd_instances{project="default",type="container"} 2'

  # test unauthenticated connections
  ! curl -k -s -X GET "https://${metrics_addr}/1.0/metrics" | grep "name=\"c1\"" || false
  ! curl -k -s -X GET "https://${metrics_addr}/1.0/metrics?project=default" | grep "name=\"c1\"" || false
  lxc config set core.metrics_authentication=false
  curl -k -s -X GET "https://${metrics_addr}/1.0/metrics" | grep "name=\"c1\""
  curl -k -s -X GET "https://${metrics_addr}/1.0/metrics?project=default" | grep "name=\"c1\""

  # Filesystem metrics should contain instance type
  curl -k -s -X GET "https://${metrics_addr}/1.0/metrics" | grep "lxd_filesystem_avail_bytes" | grep "type=\"container\""
  curl -k -s -X GET "https://${metrics_addr}/1.0/metrics?project=default" | grep "lxd_filesystem_avail_bytes" | grep "type=\"container\""

  lxc delete -f c1 c2
  lxc delete -f c3 --project foo
  lxc project rm foo
}
