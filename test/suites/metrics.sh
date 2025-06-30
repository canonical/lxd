test_metrics() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  lxc config set core.https_address "${LXD_ADDR}"

  lxc launch testimage c1
  lxc init --empty c2

  # create another container in the non default project
  lxc project create foo -c features.images=false -c features.profiles=false
  lxc launch testimage c3 --project foo

  # create but dont start a container in separate non default project to check for stopped instance accounting.
  lxc project create foo2 -c features.images=false -c features.profiles=false
  lxc init --empty c4 --project foo2

  echo "==> c1 metrics should show as the container is running"
  lxc query "/1.0/metrics" | grep "name=\"c1\""
  lxc query "/1.0/metrics?project=default" | grep "name=\"c1\""

  echo "==> c2 metrics should not be shown as the container is stopped"
  ! lxc query "/1.0/metrics" | grep "name=\"c2\"" || false
  ! lxc query "/1.0/metrics?project=default" | grep "name=\"c2\"" || false

  echo "==> Check that we can get the count of existing instances"
  lxc query /1.0/metrics | grep -xF 'lxd_instances{project="default",type="container"} 2'
  lxc query /1.0/metrics | grep -xF 'lxd_instances{project="foo",type="container"} 1'
  lxc query /1.0/metrics | grep -xF 'lxd_instances{project="foo2",type="container"} 1'
  echo "==> Ensure lxd_instances reports VM count properly (0)"
  lxc query /1.0/metrics | grep -xF 'lxd_instances{project="default",type="virtual-machine"} 0'
  lxc query /1.0/metrics | grep -xF 'lxd_instances{project="foo",type="virtual-machine"} 0'
  lxc query /1.0/metrics | grep -xF 'lxd_instances{project="foo2",type="virtual-machine"} 0'

  echo "==> c3 metrics from another project also show up for non metrics unrestricted certificate"
  lxc query "/1.0/metrics" | grep "name=\"c3\""
  lxc query "/1.0/metrics?project=foo" | grep "name=\"c3\""

  echo "==> create new certificate"
  gen_cert_and_key "metrics"

  echo "==> this should fail as the certificate is not trusted yet"
  CERTNAME=metrics my_curl -X GET "https://${LXD_ADDR}/1.0/metrics" | grep "\"error_code\":403"

  echo "==> trust newly created certificate for metrics only"
  lxc config trust add "${LXD_CONF}/metrics.crt" --type=metrics
  lxc config trust show "$(cert_fingerprint "${LXD_CONF}/metrics.crt")" | grep -xF "restricted: false"

  echo "==> c1 metrics should show as the container is running"
  CERTNAME=metrics my_curl -X GET "https://${LXD_ADDR}/1.0/metrics" | grep "name=\"c1\""
  CERTNAME=metrics my_curl -X GET "https://${LXD_ADDR}/1.0/metrics?project=default" | grep "name=\"c1\""

  echo "==> c2 metrics should not be shown as the container is stopped"
  ! CERTNAME=metrics my_curl -X GET "https://${LXD_ADDR}/1.0/metrics" | grep "name=\"c2\"" || false
  ! CERTNAME=metrics my_curl -X GET "https://${LXD_ADDR}/1.0/metrics?project=default" | grep "name=\"c2\"" || false

  echo "==> c3 metrics from another project should be shown for unrestricted certificate"
  CERTNAME=metrics my_curl -X GET "https://${LXD_ADDR}/1.0/metrics" | grep "name=\"c3\""
  CERTNAME=metrics my_curl -X GET "https://${LXD_ADDR}/1.0/metrics?project=foo" | grep "name=\"c3\""

  echo "==> internal server metrics should be shown as the certificate is not restricted"
  CERTNAME=metrics my_curl -X GET "https://${LXD_ADDR}/1.0/metrics" | grep -E "^lxd_warnings_total [0-9]+$"
  CERTNAME=metrics my_curl -X GET "https://${LXD_ADDR}/1.0/metrics?project=default" | grep -E "^lxd_warnings_total [0-9]+$"

  echo "==> make sure nothing else can be done with this certificate"
  CERTNAME=metrics my_curl -X GET "https://${LXD_ADDR}/1.0/instances" | grep "\"error_code\":403"

  metrics_addr="127.0.0.1:$(local_tcp_port)"

  lxc config set core.metrics_address "${metrics_addr}"

  echo "==> c1 metrics should be shown as the container is running"
  CERTNAME=metrics my_curl -X GET "https://${metrics_addr}/1.0/metrics" | grep "name=\"c1\""
  CERTNAME=metrics my_curl -X GET "https://${metrics_addr}/1.0/metrics?project=default" | grep "name=\"c1\""

  echo "==> c2 metrics should not be shown as the container is stopped"
  ! CERTNAME=metrics my_curl -X GET "https://${metrics_addr}/1.0/metrics" | grep "name=\"c2\"" || false
  ! CERTNAME=metrics my_curl -X GET "https://${metrics_addr}/1.0/metrics?project=default" | grep "name=\"c2\"" || false

  echo "==> c3 metrics from another project should  be shown for unrestricted metrics certificate"
  CERTNAME=metrics my_curl -X GET "https://${LXD_ADDR}/1.0/metrics" | grep "name=\"c3\""
  CERTNAME=metrics my_curl -X GET "https://${LXD_ADDR}/1.0/metrics?project=foo" | grep "name=\"c3\""

  echo "==> internal server metrics should be shown as the certificate is not restricted"
  CERTNAME=metrics my_curl -X GET "https://${metrics_addr}/1.0/metrics" | grep -E "^lxd_warnings_total [0-9]+$"
  CERTNAME=metrics my_curl -X GET "https://${metrics_addr}/1.0/metrics?project=default" | grep -E "^lxd_warnings_total [0-9]+$"

  echo "==> make sure no other endpoint is available"
  CERTNAME=metrics my_curl -X GET "https://${metrics_addr}/1.0/instances" | grep "\"error_code\":404"

  echo "==> create new (restricted) certificate"
  gen_cert_and_key "metrics-restricted"

  echo "==> trust newly created certificate for metrics only and mark it as restricted for the foo project"
  lxc config trust add "${LXD_CONF}/metrics-restricted.crt" --type=metrics --restricted --projects foo
  lxc config trust show "$(cert_fingerprint "${LXD_CONF}/metrics-restricted.crt")" | grep -xF "restricted: true"

  echo "==> c3 metrics should be showned"
  CERTNAME=metrics-restricted my_curl -X GET "https://${LXD_ADDR}/1.0/metrics?project=foo" | grep "name=\"c3\""

  echo "==> c3 metrics are shown even without specifying the project"
  # If no project is specified, all metrics accessible by the restricted certificate are returned.
  # This way, a certificate covering multiple projects can be used to scrape all the metrics in one scoop.
  CERTNAME=metrics-restricted my_curl -X GET "https://${LXD_ADDR}/1.0/metrics" | grep "name=\"c3\""

  echo "==> other projects metrics aren't visible as they aren't allowed for the restricted certificate"
  [ "$(CERTNAME=metrics-restricted my_curl -X GET "https://${LXD_ADDR}/1.0/metrics?project=default" -w "%{http_code}" -o /dev/null)" = "403" ]
  [ "$(CERTNAME=metrics-restricted my_curl -X GET "https://${LXD_ADDR}/1.0/metrics?project=foo2" -w "%{http_code}" -o /dev/null)" = "403" ]

  echo "==> c1 and c2 metrics are not visible as they are in another project"
  ! CERTNAME=metrics-restricted my_curl -X GET "https://${metrics_addr}/1.0/metrics?project=foo" | grep "name=\"c1\"" || false
  ! CERTNAME=metrics-restricted my_curl -X GET "https://${metrics_addr}/1.0/metrics?project=foo" | grep "name=\"c2\"" || false

  echo "==> Check that we can get the count of existing containers. There should be two in the default project: c1 (RUNNING) and c2 (STOPPED)"
  CERTNAME=metrics my_curl -X GET "https://${metrics_addr}/1.0/metrics" | grep -xF 'lxd_instances{project="default",type="container"} 2'
  sleep 9
  echo "==> Try again after the metric cache has expired (TTL=8s). We should still see two containers"
  CERTNAME=metrics my_curl -X GET "https://${metrics_addr}/1.0/metrics" | grep -xF 'lxd_instances{project="default",type="container"} 2'

  echo "==> test unauthenticated connections"
  ! curl -k -s -X GET "https://${metrics_addr}/1.0/metrics" | grep "name=\"c1\"" || false
  ! curl -k -s -X GET "https://${metrics_addr}/1.0/metrics?project=default" | grep "name=\"c1\"" || false
  lxc config set core.metrics_authentication=false
  curl -k -s -X GET "https://${metrics_addr}/1.0/metrics" | grep "name=\"c1\""
  curl -k -s -X GET "https://${metrics_addr}/1.0/metrics?project=default" | grep "name=\"c1\""

  echo "==> Filesystem metrics should contain instance type"
  curl -k -s -X GET "https://${metrics_addr}/1.0/metrics" | grep "lxd_filesystem_avail_bytes" | grep "type=\"container\""
  curl -k -s -X GET "https://${metrics_addr}/1.0/metrics?project=default" | grep "lxd_filesystem_avail_bytes" | grep "type=\"container\""

  echo "==> API requests metrics should be included"
  curl -k -s -X GET "https://${metrics_addr}/1.0/metrics" | grep "lxd_api_requests_completed_total" | grep "entity_type=\"server\""
  curl -k -s -X GET "https://${metrics_addr}/1.0/metrics" | grep "lxd_api_requests_completed_total" | grep "result=\"succeeded\""
  curl -k -s -X GET "https://${metrics_addr}/1.0/metrics" | grep "lxd_api_requests_ongoing" | grep "entity_type=\"server\""

  echo "==> Test lxd_api_requests_completed_total increment with different results"
  previous="$(curl -k -s -X GET "https://${metrics_addr}/1.0/metrics" | grep 'lxd_api_requests_completed_total{entity_type="instance",result="succeeded"}' | awk '{print $2}')"
  lxc list -c n # Uses /1.0/instances
  [ "$(curl -k -s -X GET "https://${metrics_addr}/1.0/metrics" | grep 'lxd_api_requests_completed_total{entity_type="instance",result="succeeded"}' | awk '{print $2}')" -eq $((previous+1)) ]

  previous="$(curl -k -s -X GET "https://${metrics_addr}/1.0/metrics" | grep 'lxd_api_requests_completed_total{entity_type="server",result="error_client"}' | awk '{print $2}')"
  ! lxc query "/not/an/endpoint" || false # returns a 404 status code and is considered a client error.
  [ "$(curl -k -s -X GET "https://${metrics_addr}/1.0/metrics" | grep 'lxd_api_requests_completed_total{entity_type="server",result="error_client"}' | awk '{print $2}')" -eq $((previous+1)) ]

  previous="$(curl -k -s -X GET "https://${metrics_addr}/1.0/metrics" | grep 'lxd_api_requests_completed_total{entity_type="instance",result="error_server"}' | awk '{print $2}')"
  lxc storage create broken dir
  rmdir "$(lxc storage get broken source)"/containers # Break the storage pool.
  ! lxc init testimage failed-container -s broken || false  # Error when creating a container on broken.
  [ "$(curl -k -s -X GET "https://${metrics_addr}/1.0/metrics" | grep 'lxd_api_requests_completed_total{entity_type="instance",result="error_server"}' | awk '{print $2}')" -eq $((previous+1)) ]

  echo "==> Test lxd_api_requests_ongoing increment and decrement"
  previous="$(curl -k -s -X GET "https://${metrics_addr}/1.0/metrics" | grep 'lxd_api_requests_ongoing{entity_type="instance"}' | awk '{print $2}')"
  lxc exec c1 -- sleep 0.5 &
  sleep 0.1
  [ "$(curl -k -s -X GET "https://${metrics_addr}/1.0/metrics" | grep 'lxd_api_requests_ongoing{entity_type="instance"}' | awk '{print $2}')" -eq $((previous+1)) ]
  wait $!
  [ "$(curl -k -s -X GET "https://${metrics_addr}/1.0/metrics" | grep 'lxd_api_requests_ongoing{entity_type="instance"}' | awk '{print $2}')" -eq "$previous" ]

  lxc storage delete broken
  lxc delete -f c1 c2
  lxc delete -f c3 --project foo
  lxc delete -f c4 --project foo2
  lxc project rm foo
  lxc project rm foo2
}
