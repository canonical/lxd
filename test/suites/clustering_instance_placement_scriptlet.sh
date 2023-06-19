test_clustering_instance_placement_scriptlet() {
  # shellcheck disable=2039,3043,SC2034
  local LXD_DIR

  setup_clustering_bridge
  prefix="lxd$$"
  bridge="${prefix}"

  # The random storage backend is not supported in clustering tests,
  # since we need to have the same storage driver on all nodes, so use the driver chosen for the standalone pool.
  poolDriver=$(lxc storage show "$(lxc profile device get default root pool)" | awk '/^driver:/ {print $2}')

  # Spawn first node
  setup_clustering_netns 1
  LXD_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_ONE_DIR}"
  ns1="${prefix}1"
  spawn_lxd_and_bootstrap_cluster "${ns1}" "${bridge}" "${LXD_ONE_DIR}" "${poolDriver}"

  # The state of the preseeded storage pool shows up as CREATED
  LXD_DIR="${LXD_ONE_DIR}" lxc storage list | grep data | grep -q CREATED

  # Add a newline at the end of each line. YAML has weird rules.
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${LXD_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  LXD_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_TWO_DIR}"
  ns2="${prefix}2"
  spawn_lxd_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${LXD_TWO_DIR}" "${poolDriver}"

  # Spawn a third node
  setup_clustering_netns 3
  LXD_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD_THREE_DIR}"
  ns3="${prefix}3"
  spawn_lxd_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${LXD_THREE_DIR}" "${poolDriver}"

  LXD_DIR="${LXD_ONE_DIR}" ensure_import_testimage

  # Check only valid scriptlets are accepted.
  ! lxc config set instances.placement.scriptlet=foo || false

  # Set basic instance placement scriptlet that logs member info and statically targets to 2nd node.
  # And by extension checks each of the scriptlet environment functions are callable.
  # Also checks that the instance_resources are provided as expected.
  cat << EOF | lxc config set instances.placement.scriptlet=-
def instance_placement(request, candidate_members):
        instance_resources = get_instance_resources()
        log_info("instance placement started: ", request, ", ", instance_resources)

        if request.reason != "new":
                return "Expecting reason new"

        if request.project != "default":
                return "Expecting project default"

        if request.config["limits.memory"] != "512MiB":
                return "Expecting config limits.memory of 512MiB"

        if instance_resources.cpu_cores != 1:
                return "Expecting cpu_cores of 1"

        if instance_resources.memory_size != 536870912:
                return "Expecting memory_size of 536870912"

        if instance_resources.root_disk_size != 209715200:
                return "Expecting root_disk_size of 209715200"

        # Log info, state and resources for each candidate member.
        for member in candidate_members:
                log_info("instance placement member: ", member)
                log_info("instance placement member resources: ", get_cluster_member_resources(member.server_name))
                log_info("instance placement member state: ", get_cluster_member_state(member.server_name))

        # Set statically target to 2nd member.
        set_target(candidate_members[1].server_name)

        return # No error.
EOF

  # Check that instance placement scriptlet is statically targeting new instances.
  # Send each request to a different cluster member to test scriptlet replication to other members.

  # Create instance with limits set on instance to appease intance_resources checks.
  LXD_DIR="${LXD_ONE_DIR}" lxc init testimage c1 -c limits.memory=512MiB -c limits.cpu=1 -d root,size=200MiB

  # Create instances with limits set on a profile to test expansion and to appease intance_resources checks.
  LXD_DIR="${LXD_TWO_DIR}" lxc profile create foo
  LXD_DIR="${LXD_TWO_DIR}" lxc profile show default | lxc profile edit foo
  LXD_DIR="${LXD_TWO_DIR}" lxc profile set foo limits.cpu=1 limits.memory=512MiB
  LXD_DIR="${LXD_TWO_DIR}" lxc profile device set foo root size=200MiB
  LXD_DIR="${LXD_TWO_DIR}" lxc init testimage c2 -p foo
  LXD_DIR="${LXD_THREE_DIR}" lxc init testimage c3 -p foo
  LXD_DIR="${LXD_ONE_DIR}" lxc info c1 | grep -q "Location: node2"
  LXD_DIR="${LXD_ONE_DIR}" lxc info c2 | grep -q "Location: node2"
  LXD_DIR="${LXD_ONE_DIR}" lxc info c3 | grep -q "Location: node2"
  LXD_DIR="${LXD_ONE_DIR}" lxc delete -f c1 c2 c3
  LXD_DIR="${LXD_ONE_DIR}" lxc profile delete foo

  # Set instance placement scriptlet that returns an error and test instance creation fails.
  cat << EOF | lxc config set instances.placement.scriptlet=-
def instance_placement(request, candidate_members):
        log_error("instance placement not allowed") # Log placement error.

        fail("Instance not allowed") # Fail to prevent instance creation.
EOF

  ! LXD_DIR="${LXD_ONE_DIR}" lxc init testimage c1 || false

  # Set instance placement scriptlet containing runtime error in it and test instance creation fails.
  cat << EOF | lxc config set instances.placement.scriptlet=-
def instance_placement(request, candidate_members):
        log_info("Accessing invalid field ", candidate_members[4])

        return
EOF

  ! LXD_DIR="${LXD_ONE_DIR}" lxc init testimage c1 || false

  # Set instance placement scriptlet to one that sets an invalid cluster member target.
  # Check that instance placement uses LXD's built in logic instead (as if setTarget hadn't been called at all).
  cat << EOF | lxc config set instances.placement.scriptlet=-
def instance_placement(request, candidate_members):
        # Set invalid member target.
        result = set_target("foo")
        log_warn("Setting invalid member target result: ", result)

        return
EOF

  LXD_DIR="${LXD_ONE_DIR}" lxc init testimage c1 -c cluster.evacuate=migrate
  LXD_DIR="${LXD_ONE_DIR}" lxc info c1 | grep -q "Location: node1"

  # Set basic instance placement scriptlet that statically targets to 3rd member.
  cat << EOF | lxc config set instances.placement.scriptlet=-
def instance_placement(request, candidate_members):
        log_info("instance placement started: ", request)

        if request.reason != "evacuation":
                return "Expecting reason evacuation"

        # Log info, state and resources for each candidate member.
        for member in candidate_members:
                log_info("instance placement member: ", member)

        # Set statically target to 3rd member.
        # Note: We expect the candidate members to not contain the member being evacuated, and thus the 3rd
        # member is the 2nd entry in the candidate_members list now.
        set_target(candidate_members[1].server_name)

        return # No error.
EOF

  # Evacuate member with instance and check its moved to 2nd member.
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster evacuate node1 --force
  LXD_DIR="${LXD_ONE_DIR}" lxc cluster show node1 | grep -q "status: Evacuated"
  LXD_DIR="${LXD_ONE_DIR}" lxc info c1 | grep -q "Location: node3"
  LXD_DIR="${LXD_ONE_DIR}" lxc delete -f c1

  # Delete the storage pool
  printf 'config: {}\ndevices: {}' | LXD_DIR="${LXD_ONE_DIR}" lxc profile edit default
  LXD_DIR="${LXD_ONE_DIR}" lxc storage delete data

  # Shut down cluster
  LXD_DIR="${LXD_ONE_DIR}" lxd shutdown
  LXD_DIR="${LXD_TWO_DIR}" lxd shutdown
  LXD_DIR="${LXD_THREE_DIR}" lxd shutdown
  sleep 0.5
  rm -f "${LXD_ONE_DIR}/unix.socket"
  rm -f "${LXD_TWO_DIR}/unix.socket"
  rm -f "${LXD_THREE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_lxd "${LXD_ONE_DIR}"
  kill_lxd "${LXD_TWO_DIR}"
  kill_lxd "${LXD_THREE_DIR}"

  # shellcheck disable=SC2034
  LXD_NETNS=
}
