test_clustering_move() {
  # shellcheck disable=SC2034
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}"

  # Add a newline at the end of each line. YAML has weird rules.
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${LXD_ONE_DIR}"

  # Spawn a third node
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${LXD_THREE_DIR}" "${LXD_ONE_DIR}"

  # Preparation
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group create foobar1
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group assign node1 foobar1,default

  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group create foobar2
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group assign node2 foobar2,default

  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group create foobar3
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster group assign node3 foobar3,default

  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty c1 --target node1
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty c2 --target node2
  LXD_DIR="${LXD_ONE_DIR}" lxc init --empty c3 --target node3

  LXD_DIR="${LXD_ONE_DIR}" lxc project create test-project --force-local # Create test project using unix socket.

  # Set up a fine-grained TLS identity.
  token="$(LXD_DIR=${LXD_ONE_DIR} lxc auth identity create tls/test --quiet)"
  lxc remote add cluster 100.64.1.101:8443 --token="${token}"
  lxc remote set-url cluster https://100.64.1.102:8443

  # Make the identity a member of a group that has minimal permissions for moving the instances.
  LXD_DIR=${LXD_ONE_DIR} lxc auth group create instance-movers
  LXD_DIR=${LXD_ONE_DIR} lxc auth identity group add tls/test instance-movers
  LXD_DIR=${LXD_ONE_DIR} lxc auth group permission add instance-movers project default can_create_instances # Required, since a move constitutes an initial copy.
  LXD_DIR=${LXD_ONE_DIR} lxc auth group permission add instance-movers project test-project can_create_instances # Required, since a move constitutes an initial copy.
  LXD_DIR=${LXD_ONE_DIR} lxc auth group permission add instance-movers instance c1 can_edit project=default
  LXD_DIR=${LXD_ONE_DIR} lxc auth group permission add instance-movers instance c1 can_view project=default

  # Perform default move tests falling back to the built in logic of choosing the node
  # with the least number of instances when targeting a cluster group.
  lxc move cluster:c1 --target node2

  # c1 can be moved to a new target project.
  lxc move cluster:c1 --target-project test-project
  lxc move cluster:c1 --target-project default --project test-project

  lxc move cluster:c1 --target @foobar1
  [ "$(lxc list -f csv -c L cluster:c1)" = "node1" ]

  # c1 can be moved within the same cluster group if it has multiple members
  current_location="$(lxc query cluster:/1.0/instances/c1 | jq -r '.location')"
  lxc move cluster:c1 --target=@default
  lxc query cluster:/1.0/instances/c1 | jq -re ".location != \"$current_location\""
  current_location="$(lxc query cluster:/1.0/instances/c1 | jq -r '.location')"
  lxc move cluster:c1 --target=@default
  lxc query cluster:/1.0/instances/c1 | jq -re ".location != \"$current_location\""

  # c1 cannot be moved within the same cluster group if it has a single member
  lxc move cluster:c1 --target=@foobar3
  [ "$(lxc list -f csv -c L cluster:c1)" = "node3" ]
  ! lxc move cluster:c1 --target=@foobar3 || false

  # Perform standard move tests using the `scheduler.instance` cluster member setting.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster set node2 scheduler.instance=group
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster set node3 scheduler.instance=manual

  # At this stage we have:
  # - node1 in group foobar1,default accepting all instances
  # - node2 in group foobar2,default accepting group-only targeting
  # - node3 in group foobar3,default accepting manual targeting only
  # - c1 is deployed on node1
  # - c2 is deployed on node2
  # - c3 is deployed on node3

  # c1 can be moved to node2 by group targeting.
  lxc move cluster:c1 --target=@foobar2
  [ "$(lxc list -f csv -c L cluster:c1)" = "node2" ]

  # c2 can be moved to node1 by manual targeting.
  LXD_DIR=${LXD_ONE_DIR} lxc auth group permission add instance-movers instance c2 can_edit project=default
  LXD_DIR=${LXD_ONE_DIR} lxc auth group permission add instance-movers instance c2 can_view project=default
  lxc move cluster:c2 --target=node1
  [ "$(lxc list -f csv -c L cluster:c2)" = "node1" ]

  # c1 cannot be moved to node3 by group targeting.
  ! lxc move cluster:c1 --target=@foobar3 || false

  # c2 can be moved to node2 by manual targeting.
  lxc move cluster:c2 --target=node2
  [ "$(lxc list -f csv -c L cluster:c2)" = "node2" ]

  # c3 can be moved to node1 by manual targeting.
  LXD_DIR=${LXD_ONE_DIR} lxc auth group permission add instance-movers instance c3 can_edit project=default
  LXD_DIR=${LXD_ONE_DIR} lxc auth group permission add instance-movers instance c3 can_view project=default
  lxc move cluster:c3 --target=node1
  [ "$(lxc list -f csv -c L cluster:c3)" = "node1" ]

  # c3 can be moved back to node by by manual targeting.
  lxc move cluster:c3 --target=node3
  [ "$(lxc list -f csv -c L cluster:c3)" = "node3" ]

  # Clean up
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster unset node2 scheduler.instance
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster unset node3 scheduler.instance
  lxc move cluster:c1 --target node1

  # Perform project restriction tests.
  # At this stage we have:
  # - node1 in group foobar1,default
  # - node2 in group foobar2,default
  # - node3 in group foobar3,default
  # - c1 is deployed on node1
  # - c2 is deployed on node2
  # - c3 is deployed on node3
  # - default project restricted to cluster groups foobar1,foobar2
  LXD_DIR="${LXD_ONE_DIR}" lxc project set default restricted=true restricted.networks.uplinks="${bridge}"
  LXD_DIR="${LXD_ONE_DIR}" lxc project set default restricted.cluster.groups=foobar1,foobar2

  # Moving to an unlisted group fails.
  ! lxc move cluster:c1 --target @foobar3 || false

  # Moving directly to another node within the cluster group fails because the caller does not have
  # can_override_cluster_target_restriction on server.
  ! lxc move cluster:c2 --target node2 || false

  # After adding the entitlement, moving to a cluster member that is not in the list of restricted cluster groups will fail.
  LXD_DIR="${LXD_ONE_DIR}" lxc auth group permission add instance-movers server can_override_cluster_target_restriction
  ! lxc move cluster:c2 --target node3 || false

  # Moving instances in between the restricted groups (note that targeting members directly now works after adding the entitlement).
  lxc move cluster:c1 --target node2
  lxc move cluster:c2 --target @foobar1
  lxc move cluster:c3 --target node1

  # Cleanup
  lxc remote remove cluster
  LXD_DIR="${LXD_ONE_DIR}" lxc delete c1 c2 c3
  LXD_DIR="${LXD_ONE_DIR}" lxc auth group delete instance-movers
  LXD_DIR="${LXD_ONE_DIR}" lxc auth identity delete tls/test
  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  sleep 0.5
  rm -f "${LXD_THREE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
  kill_lxd "${LXD_THREE_DIR}"
}
