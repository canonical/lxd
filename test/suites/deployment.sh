test_deployment() {
    current_project=$(lxc project list --format csv | awk -F, '/\(current\)/ {print $1}' | sed 's/ (current)//')
    default_profile=$(lxc profile show default)
    deployment_project="deployment-test"
    export deployment_project
    lxc project create "${deployment_project}" \
        -c features.images=true \
        -c features.profiles=true \
        -c features.storage.volumes=true \
        -c features.networks=true

    lxc project switch "${deployment_project}"
    echo "${default_profile}" | lxc profile edit default

    test_simple_deployment
    test_deployment_shape
    test_deployment_key
    test_deployment_shape_instance
    test_deployment_outside_governor

    # delete remaining images and profiles in project
    lxc image list --format csv | awk -F',' '{print $2}' | xargs -I{} lxc image delete {}
    lxc profile list --format csv | awk -F',' '$1 !~ /default/ {print $1}' | xargs -I{} lxc profile delete {}

    lxc project switch "${current_project}"
    lxc project delete "${deployment_project}"
}

test_simple_deployment() {
    # Creating empty deployments
    lxc deployment create dep1
    lxc deployment create dep2 --description "This is a test description for dep2"
    lxc deployment create dep3 --description "This is a test description for dep3" --governor-webhook-url http://0.0.0.0/scale
    lxc deployment create dep4 \
        --description "Test description for dep4" \
        --governor-webhook-url http://0.0.0.0/scale \
        user.foo=bar user.bar=baz

    # Creating deployment from stdin.
    cat <<EOF | lxc deployment create dep5
description: This is a test description for dep5
governor_webhook_url: http://0.0.0.0/scale
config:
  user.foo: blah
  user.foo.bar: blah2
EOF

    # Listing deployments
    lxc deployment list --format csv | grep dep1
    lxc deployment list --format csv | grep dep2
    lxc deployment list --format csv | grep dep3
    lxc deployment list --format csv | grep dep4

    # Showing a deployment
    lxc deployment show dep4 | grep -q 'user.foo: bar'
    lxc deployment show dep4 | grep -q 'governor_webhook_url: http://0.0.0.0/scale'

    # Getting a deployment key value / property
    lxc deployment get dep4 user.foo | grep -q 'bar'
    lxc deployment get dep4 governor_webhook_url --property | grep -q 'http://0.0.0.0/scale'

    # Setting/Unsetting a deployment key value / property
    lxc deployment set dep4 governor_webhook_url http://127.0.0.0/scale --property # TODO: not working
    lxc deployment get dep4 governor_webhook_url --property | grep -q 'http://127.0.0.0/scale'

    # Editing a deployment
    cat <<EOF | lxc deployment edit dep4
description: New description for dep4
governor_webhook_url: http://0.0.0.0/scale
config:
  user.foo: blah2
  user.foo.bar: blah3
EOF

    # Deleting a deployment
    lxc deployment delete dep1
    ! lxc deployment delete depNotFound || false
    lxc deployment delete dep2
    lxc deployment delete dep3
    lxc deployment delete dep4
    lxc deployment delete dep5

    # Check that the list of deployment is empty
    [ "$(lxc deployment list --format csv | wc -l)" -eq 0 ]
}

test_deployment_shape() {
    # Create a deployment for the shapes
    lxc deployment create dep1

    # Create shapes
    ! lxc deployment shape create dep1 shape1 || false # Scaling values cannot both be zero.
    lxc deployment shape create dep1 shape2 \
        --description "This is a test description for shape2" \
        --scaling-min 2 \
        --scaling-max 10

    ! lxc deployment shape create dep1 shape2 \
        --description "This is a test description for shape2" \
        --scaling-min 4 \
        --scaling-max 3 || false # max < min

    ! lxc deployment shape create depNotFound shape1 || false # deployment not found
    ! lxc deployment shape create dep1 shape1 || false # existing shape

    # Creating deployment shape from stdin.
    cat <<EOF | lxc deployment shape create dep1 shape3
description: This is a test description for shape3
scaling_minimum: 2
scaling_maximum: 8
config:
  user.foo: blah
  user.bar: blah2
EOF

    # Listing shapes
    lxc deployment shape list dep1 --format csv | grep -e shape1 -e shape2 -e shape3

    # Showing a shape
    lxc deployment shape show dep1 shape2 | grep -e 'scaling_minimum: 1' -e 'scaling_maximum: 10'

    # Getting a shape key value / property
    lxc deployment shape get dep1 shape3 scaling_minimum --property | grep -q '2'
    lxc deployment shape get dep1 shape3 user.foo | grep -q 'blah'

    # Setting/Unsetting a shape key value / property
    lxc deployment shape set dep1 shape3 scaling_minimum 3 --property
    lxc deployment shape get dep1 shape3 scaling_minimum --property | grep -q '3'

    # Editing a shape
    cat <<EOF | lxc deployment shape edit dep1 shape3
description: New description for shape3
scaling_maximum: 8
config:
  new.user.foo: blah2
  new.user.foo.bar: blah3
EOF

    lxc deployment shape get dep1 shape3 description --property | grep -q 'New description for shape3'
    lxc deployment shape get dep1 shape3 new.user.foo | grep -q 'blah2'
    lxc deployment shape get dep1 shape3 new.user.foo.bar | grep -q 'blah3'

    # Rename a shape
    lxc deployment shape rename dep1 shape3 new-shape
    lxc deployment shape list dep1 --format csv | grep new-shape

    # deleting a shape
    ! lxc deployment shape delete dep1 shape1 || false # This shape was never created.
    ! lxc deployment shape delete depNotFound shape1 || false # deployment not found
    ! lxc deployment shape delete dep1 shapeNotFound || false # shape not found
    lxc deployment shape delete dep1 shape2
    lxc deployment shape delete dep1 new-shape

    # Creating a shape from an image
    ensure_import_testimage

    lxc deployment shape create dep1 shape1 --from-image testimage --scaling-max 1
    lxc deployment shape show dep1 shape1 | grep -e 'type: image' -e 'alias: testimage' -e 'protocol: simplestreams'
    ! lxc deployment shape create dep1 shape1 --from-image wrongimage || false # image not found

    # Creating a shape from an instance profile
    cat <<EOF | lxc profile create test-profile
config:
  user.user-data: |
    #cloud-config
    packages:
      - htop
      - vim
    write_files:
      - content: |
          export VAR=VALUE
        path: /etc/environment
        permissions: '0644'
devices:
  eth0:
    name: eth0
    nictype: bridged
    parent: lxdbr0
    type: nic
  root:
    path: /
    pool: default
    type: disk
    size: 8GB
description: Test LXD Profile for a test deployment shape
name: example-profile
EOF

    lxc deployment shape create dep1 shape2 --from-profile test-profile --scaling-max 1
    lxc deployment shape show dep1 shape2 | awk '
BEGIN { found=0; inside=0; }
/^devices:/ { inside=1; }
inside && /^$/ { exit found; }
inside && /eth0:/ && /name: eth0/ && /nictype: bridged/ && /parent: lxdbr0/ && /type: nic/ { found=1; }
'

    # Creating a shape from an image and an instance profile
    # while specifying that the shape must create vm instances (and not containers that are set by default)
    lxc deployment shape create dep1 complete-shape --from-profile test-profile --from-image testimage --vm --scaling-max 1
    lxc deployment shape show dep1 complete-shape | awk '
BEGIN { found=0; inside=0; }
/^instance_template:/ { inside=1; }
inside && /type: virtual-machine/ { found=1; }
'

    # Deleting the deployment shapes
    lxc deployment shape delete dep1 shape1
    lxc deployment shape delete dep1 shape2
    lxc deployment shape delete dep1 complete-shape

    # Deleting the deployment
    lxc deployment delete dep1

    # Delete the test profile
    lxc profile delete test-profile
}

test_deployment_key() {
    # Creating a new deployment.
    cat <<EOF | lxc deployment create dep1
description: This is a test description for dep1
governor_webhook_url: http://0.0.0.0/scale
config:
  user.foo: blah
  user.foo.bar: blah2
EOF

    # In order to create a deployment key, we need to create a new private key / certificate pair.
    openssl req -x509 -newkey rsa:2048 -keyout "${TEST_DIR}/dep1-key1.key" -nodes -out "${TEST_DIR}/dep1-key1.crt" -subj "/CN=lxd.local"
    # And we need to add it as a trusted key for the deployments.
    lxc config trust add "${TEST_DIR}/dep1-key1.crt" --type=deployments
    # Fetch the fingerprint of the trusted certificate.
    fingerprint_short=$(lxc config trust list --format csv | awk -F, '$2 == "dep1-key1.crt" {print $4}')
    cmd=$(lxc config trust show "${fingerprint_short}")
    fingerprint_full=$(echo "${cmd}" | grep "^fingerprint:" | awk '{print $2}')
    # Then we can create a deployment key and link it to the trusted certificate.
    lxc deployment key create dep1 key1 "${fingerprint_full}" --role rw

    # Listing deployment keys
    lxc deployment key list dep1 --format csv | grep key1

    # Showing a deployment key
    lxc deployment key show dep1 key1 | grep -q 'certificate_fingerprint:'

    # Getting a deployment key value / property
    lxc deployment key get dep1 key1 certificate_fingerprint | grep -q "${fingerprint_full}"

    # Setting/Unsetting a deployment key value / property
    lxc deployment key set dep1 key1 role=ro
    lxc deployment key set dep1 key1 description="This is a test description for key1"
    lxc deployment key get dep1 key1 description | grep -q "This is a test description for key1"
    lxc deployment key unset dep1 key1 description
    ! lxc deployment key get dep1 key1 description | grep -q "This is a test description for key1" || false

    # Editing a deployment key
    cat <<EOF | lxc deployment key edit dep1 key1
description: New description for key1
role: ro
EOF

    lxc deployment key get dep1 key1 description | grep -q "New description for key1"
    lxc deployment key get dep1 key1 role | grep -q "ro"

    # Rename a deployment key
    lxc deployment key rename dep1 key1 new-key
    lxc deployment key list dep1 --format csv | grep -q "new-key"

    # Deleting a deployment key
    lxc deployment key delete dep1 new-key
    ! lxc deployment key delete depNotFound key1 || false # deployment not found

    # Deleting the deployment
    lxc deployment delete dep1

    # Deleting the trusted certificate
    lxc config trust remove "${fingerprint_short}"
}

test_deployment_shape_instance() {
    #  Create a deployment
    lxc deployment create dep1 --description "dep1 desc"

    # Create a deployment key
    openssl req -x509 -newkey rsa:2048 -keyout "${TEST_DIR}/dep1-key1.key" -nodes -out "${TEST_DIR}/dep1-key1.crt" -subj "/CN=lxd.local"
    lxc config trust add "${TEST_DIR}/dep1-key1.crt" --type=deployments
    fingerprint_short=$(lxc config trust list --format csv | awk -F, '$2 == "dep1-key1.crt" {print $4}')
    cmd=$(lxc config trust show "${fingerprint_short}")
    fingerprint_full=$(echo "${cmd}" | grep "^fingerprint:" | awk '{print $2}')
    lxc deployment key create dep1 key1 "${fingerprint_full}" --role rw

    # Create an instance profile to be used by the deployment shape
    cat <<EOF | lxc profile create test-profile
devices:
  eth0:
    name: eth0
    nictype: bridged
    parent: lxdbr0
    type: nic
  root:
    path: /
    pool: default
    type: disk
    size: 8GB
description: Test LXD Profile for test deployment shapes
name: example-profile
EOF

    # Create a valid deployment shape
    ensure_import_testimage
    lxc deployment shape create dep1 shape1-containers --description "shape1-containers desc" \
        --scaling-min 2 \
        --scaling-max 4 \
        --from-profile test-profile \
        --from-image testimage

    # Create an instance within the deployment shape instance
    lxc deployment shape instance launch dep1 shape1-containers c1
    lxc deployment shape instance launch dep1 shape1-containers c2
    lxc deployment shape instance launch dep1 shape1-containers c3
    lxc deployment shape instance launch dep1 shape1-containers c4 # max instances reached
    ! lxc deployment shape instance launch dep1 shape1-containers c5 || false # above max instances supported in shape-containers
    sleep 10
    lxc deployment shape instance delete dep1 shape1-containers c3 # We can delete an instance using the deployment API and the scaling constaints will be enforced
    sleep 10

    # Check that the used_by field is updated
    lxc deployment show dep1 | awk '
BEGIN { found=0; counter=0; }
/used_by:/ { found=1; next; }
found && /^- \/1.0\/deployments\/dep1\/keys\/key1$/ { counter++; next; }
found && /^- \/1.0\/instances\/c1$/ { counter++; next; }
found && /^- \/1.0\/instances\/c2$/ { counter++; next; }
found && /^- \/1.0\/instances\/c4$/ { counter++; next; }
found && /^$/ { if (counter == 4) print "Matched!"; exit; }
'

    cmd=$(lxc deployment list --format csv)
    used_by_num=$(echo "${cmd}" | awk -F, '{print $4}' | tr -d '"')
    # The deployment 'dep1' is used by 3 instances (c1, c2, c4) and one deployment key (key1)
    [ "${used_by_num}" -eq 4 ]

    lxc deployment shape instance list dep1 shape1-containers --format yaml | grep -e 'name: c1' -e 'name: c2' -e 'name: c4'

    # Using the root API, we can delete an instance and the scaling constaints won't be enforced
    lxc delete c1 -f
    lxc delete c2 -f
    lxc delete c4 -f
    lxc deployment key delete dep1 key1
    lxc config trust remove "${fingerprint_short}"
    lxc deployment shape delete dep1 shape1-containers
    lxc deployment delete dep1
}

test_deployment_outside_governor() {
    # Create a deployment
    lxc deployment create real-dep \
        --description "real situation deployment"

    # Create a deployment key (will be used by the governor)
    openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:secp384r1 -sha384 -keyout "${TEST_DIR}/governor-deployment.key" -nodes -out "${TEST_DIR}/governor-deployment.crt" -days 3650 -subj "/CN=deployments.local"

    lxc config trust add "${TEST_DIR}/governor-deployment.crt" --type=deployments
    fingerprint_short=$(lxc config trust list --format csv | awk -F, '$2 == "governor-deployment.crt" {print $4}')
    cmd=$(lxc config trust show "${fingerprint_short}")
    fingerprint_full=$(echo "${cmd}" | grep "^fingerprint:" | awk '{print $2}')
    lxc deployment key create real-dep governor-key "${fingerprint_full}" --role rw # the governor has read-write access on the deployment's instances.

    # Create two deployment shapes (each one with a base configuration + a custom disk)
    mkdir -p "${TEST_DIR}/deployment-source-small"
    mkdir -p "${TEST_DIR}/deployment-source-medium"

    ensure_import_testimage
    cat <<EOF | lxc profile create small-instance-profile
devices:
  eth0:
    name: eth0
    nictype: bridged
    parent: lxdbr0
    type: nic
  root:
    path: /
    pool: default
    type: disk
    size: 8GB
description: This profile describe a small instance
name: small-instance-profile
EOF
    lxc profile device add small-instance-profile small-disk-device disk source="${TEST_DIR}/deployment-source-small" path=/mnt

    cat <<EOF | lxc profile create medium-instance-profile
devices:
  eth0:
    name: eth0
    nictype: bridged
    parent: lxdbr0
    type: nic
  root:
    path: /
    pool: default
    type: disk
    size: 8GB
description: This profile describe a medium instance
name: medium-instance-profile
EOF
    lxc profile device add medium-instance-profile medium-disk-device disk source="${TEST_DIR}/deployment-source-medium" path=/mnt

    lxc deployment shape create real-dep shape-small-containers \
        --description "shape spawning small containers" \
        --scaling-min 1 \
        --scaling-max 4 \
        --from-profile small-instance-profile \
        --from-image testimage

    lxc deployment shape create real-dep shape-medium-containers \
        --description "shape spawning medium containers" \
        --scaling-min 2 \
        --scaling-max 6 \
        --from-profile medium-instance-profile \
        --from-image testimage \

    # Make the LXD server listen on port 8443 for HTTPS connections
    lxc config set core.https_address "${LXD_ADDR}"

    # Compile the governor program
    (
        cd rest-governor-client || return
        # Use -buildvcs=false here to prevent git complaining about untrusted directory when tests are run as root.
        go build -v -buildvcs=false ./...
    )

    # Start the governor and wait for it to reconcile with the desired deployment state
    rest-governor-client/rest-governor-client \
        --governor-deployment-key "${TEST_DIR}/governor-deployment.key" \
        --governor-deployment-certificate "${TEST_DIR}/governor-deployment.crt" \
        --deployment real-dep \
        --project "${deployment_project}" \
        --server-addr "https://${LXD_ADDR}"

    # Check that the governor has properly scaled the instances (for now,
    # as the governor is dumb, it'll create instances to reach the upper scaling-max limit,
    # so we need to acknowledge these numbers).
    #
    # Let's agree on the following rules:
    # - The created containers will be named <deploymentName>-<deploymentShapeName>-c0, <deploymentName>-<deploymentShapeName>-c1, etc.
    desired_small_containers="real-dep-shape-small-containers-c0 real-dep-shape-small-containers-c1 real-dep-shape-small-containers-c2 real-dep-shape-small-containers-c3"
    desired_medium_containers="real-dep-shape-medium-containers-c0 real-dep-shape-medium-containers-c1 real-dep-shape-medium-containers-c2 real-dep-shape-medium-containers-c3 real-dep-shape-medium-containers-c4 real-dep-shape-medium-containers-c5"

    live_containers=$(lxc ls -c ns type=container --format csv)

    # Check if each container exists and is running
    all_containers_running=true
    for container in $desired_small_containers; do
        status=$(echo "${live_containers}" | awk -F, -v name="${container}" '$1 == name {print $2}')
        if [ -z "$status" ] ; then
            echo "${container} does not exist."
            all_containers_running=false
        elif [ "$status" != "RUNNING" ] ; then
            echo "${container} is not running."
            all_containers_running=false
        fi
    done

    for container in $desired_medium_containers; do
        status=$(echo "${live_containers}" | awk -F, -v name="${container}" '$1 == name {print $2}')
        if [ -z "$status" ] ; then
            echo "${container} does not exist."
            all_containers_running=false
        elif [ "$status" != "RUNNING" ] ; then
            echo "${container} is not running."
            all_containers_running=false
        fi
    done

    if [ "${all_containers_running}" = false ] ; then
        echo "Not all containers are running."
        false
    fi

    # Deleting the deployment trusted certificate
    lxc config trust remove "${fingerprint_short}"

    for container in $desired_small_containers; do
        lxc delete "${container}" -f
    done

    for container in $desired_medium_containers; do
        lxc delete "${container}" -f
    done

    lxc deployment shape delete real-dep shape-small-containers
    lxc deployment shape delete real-dep shape-medium-containers
    lxc profile delete small-instance-profile
    lxc profile delete medium-instance-profile
    lxc deployment delete real-dep
}