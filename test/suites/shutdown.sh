test_shutdown() {
    ensure_import_testimage
    lxd_backend=$(storage_backend "$LXD_DIR")

    scenario_name="scenario1"
    echo "$scenario_name"
    echo "- LXD shutdown sequence with no instances running."
    echo "Expected behavior: LXD should shutdown without any issues."
    echo "----------------------------------------------------------"

    lxd_shutdown_restart "$scenario_name" "$LXD_DIR"

    echo "LXD restarted started successfully."
    rm "$scenario_name.log"

    scenario_name="scenario2"
    echo "$scenario_name"
    echo "- LXD shutdown sequence with instances running."
    echo "- No pending operations on the instances."
    echo "Expected behavior: LXD should shutdown without any issues."
    echo "----------------------------------------------------------"

    if ! create_instances 2; then
        echo "Failed to create instances."
        exit 1
    fi

    lxd_shutdown_restart "$scenario_name" "$LXD_DIR"

    # Check the logs for expected messages that should be shown in the LXD shutdown sequence.
    # The order of the expected messages does not matter.
    expected_msgs=(
        'Starting shutdown sequence'
        'Stopping daemon storage volumes'
        'Daemon storage volumes unmounted'
        'Operations deleted from the database'
        'Closing the database'
    )
    if ! check_log_presence "$scenario_name.log" "${expected_msgs[@]}"; then
        echo "Failed to find expected messages in the log file."
        exit 1
    fi

    # Cleanup
    delete_instances 2
    rm "$scenario_name.log"

    scenario_name="scenario3"
    echo "$scenario_name"
    echo "- LXD shutdown sequence with instances running."
    echo "- Pending operations on some of them."
    echo "- All instances have the same boot priority."
    echo "Expected behavior: LXD should shutdown without any issues."
    echo "----------------------------------------------------------"

    if ! create_instances 4; then
        echo "Failed to create instances."
        exit 1
    fi

    # Define a global timeout for the shutdown sequence of 1 minute.
    lxc config set core.shutdown_timeout=1

    # With this configuration, all instances should be
    # shutdown gracefully because their operation should
    # finish before the global shutdown sequence timeout of 1 minute is reached.
    declare -A instance_ops_duration
    instance_ops_duration["i1"]=5s
    instance_ops_duration["i2"]=8s
    instance_ops_duration["i3"]=10s

    for instance_name in "${!instance_ops_duration[@]}"; do
        duration_seconds="${instance_ops_duration[$instance_name]}"
        echo "Starting operation for instance $instance_name for $duration_seconds seconds"
        lxd_websocket_operation "$instance_name" "$duration_seconds" &
    done

    # Wait for all instance operations to be registered before initiating the shutdown sequence.
    sleep 1
    # Initiate the LXD shutdown sequence.
    # This call should block until before the global timeout is reached.
    lxd_shutdown_restart "$scenario_name" "$LXD_DIR"

    # Check the logs for expected messages that should be shown in the LXD shutdown sequence.
    # The order of the expected messages does not matter.
    expected_msgs=(
        'Starting shutdown sequence'
        '"Stopping instances" stopPriority=0'
        '"Instance received for shutdown" instance=i1 project=default'
        '"Instance received for shutdown" instance=i2 project=default'
        '"Instance received for shutdown" instance=i3 project=default'
        '"Instance received for shutdown" instance=i4 project=default'
        '"Instance shutdown complete" instance=i1 project=default'
        '"Instance shutdown complete" instance=i2 project=default'
        '"Instance shutdown complete" instance=i3 project=default'
        '"Instance shutdown complete" instance=i4 project=default'
        'Stopping daemon storage volumes'
        'Daemon storage volumes unmounted'
        'Operations deleted from the database'
        'Closing the database'
    )
    if ! check_log_presence "$scenario_name.log" "${expected_msgs[@]}"; then
        echo "Failed to find expected messages in the log file."
        exit 1
    fi

    # Cleanup
    delete_instances 4
    rm "$scenario_name.log"

    scenario_name="scenario4"
    echo "$scenario_name"
    echo "- LXD shutdown sequence with instances running."
    echo "- Pending operations on some of them."
    echo "- Some instances have a boot priority, some don't."
    echo "Expected behavior: LXD should shutdown without any issues."
    echo "----------------------------------------------------------"

    if ! create_instances 4; then
        echo "Failed to create instances."
        exit 1
    fi

    # Define a global timeout for the shutdown sequence of 1 minute.
    lxc config set core.shutdown_timeout=1

    # With this configuration, all instances should be
    # shutdown gracefully because their operation should
    # finish before the global shutdown sequence timeout of 1 minute is reached.
    declare -A instance_ops_duration
    instance_ops_duration["i1"]=5s
    instance_ops_duration["i2"]=8s
    instance_ops_duration["i3"]=10s

    lxc config set i3 boot.stop.priority 1
    lxc config set i4 boot.stop.priority 1

    for instance_name in "${!instance_ops_duration[@]}"; do
        duration_seconds="${instance_ops_duration[$instance_name]}"
        echo "Starting operation for instance $instance_name for $duration_seconds seconds"
        lxd_websocket_operation "$instance_name" "$duration_seconds" &
    done

    # Wait for all instance operations to be registered before initiating the shutdown sequence.
    sleep 1
    # Initiate the LXD shutdown sequence.
    # This call should block until before the global timeout is reached.
    lxd_shutdown_restart "$scenario_name" "$LXD_DIR"

    # Check the logs for expected messages that should be shown in the LXD shutdown sequence.
    # The order of the expected messages does not matter.
    expected_msgs=(
        'Starting shutdown sequence'
        '"Stopping instances" stopPriority=0'
        '"Instance received for shutdown" instance=i1 project=default'
        '"Instance received for shutdown" instance=i2 project=default'
        '"Instance received for shutdown" instance=i3 project=default'
        '"Instance received for shutdown" instance=i4 project=default'
        '"Instance shutdown complete" instance=i1 project=default'
        '"Instance shutdown complete" instance=i2 project=default'
        '"Instance shutdown complete" instance=i3 project=default'
        '"Instance shutdown complete" instance=i4 project=default'
        'Stopping daemon storage volumes'
        'Daemon storage volumes unmounted'
        'Operations deleted from the database'
        'Closing the database'
    )
    if ! check_log_presence "$scenario_name.log" "${expected_msgs[@]}"; then
        echo "Failed to find expected messages in the log file."
        exit 1
    fi

    # the order between i1 and i2 is not guaranteed.
    ordered_msgs=(
        '"Stopping instances" stopPriority=1'
        '"Instance received for shutdown" instance=i4 project=default'
        '"Instance shutdown complete" instance=i4 project=default'
        '"Instance received for shutdown" instance=i3 project=default'
        '"Instance shutdown complete" instance=i3 project=default'
        '"Stopping instances" stopPriority=0'
    )
    if ! check_log_order "$scenario_name.log" "${ordered_msgs[@]}"; then
        echo "Failed to find given messages in the right order in the log file."
        exit 1
    fi

    # Cleanup
    delete_instances 4
    rm "$scenario_name.log"

    scenario_name="scenario5"
    echo "$scenario_name"
    echo "- LXD shutdown sequence with instances running."
    echo "- Pending operations on some of them."
    echo "- Among the busy instances, they have different boot priorities."
    echo "Expected behavior: LXD should shutdown without any issues."
    echo "----------------------------------------------------------------"

    if ! create_instances 5; then
        echo "Failed to create instances."
        exit 1
    fi

    lxc config set i1 boot.stop.priority 0
    lxc config set i2 boot.stop.priority 1
    lxc config set i3 boot.stop.priority 2
    lxc config set i4 boot.stop.priority 2
    lxc config set i5 boot.stop.priority 3

    lxc config set core.shutdown_timeout=1

    declare -A instance_ops_duration
    instance_ops_duration["i1"]=5s
    instance_ops_duration["i3"]=8s
    instance_ops_duration["i4"]=8s
    instance_ops_duration["i5"]=12s

    for instance_name in "${!instance_ops_duration[@]}"; do
        duration_seconds="${instance_ops_duration[$instance_name]}"
        echo "Starting operation for instance $instance_name for $duration_seconds seconds"
        lxd_websocket_operation "$instance_name" "$duration_seconds" &
    done

    sleep 1
    lxd_shutdown_restart "$scenario_name" "$LXD_DIR"

    expected_msgs=(
        'Starting shutdown sequence'
        '"Stopping instances" stopPriority=3'
        '"Stopping instances" stopPriority=2'
        '"Stopping instances" stopPriority=1'
        '"Stopping instances" stopPriority=0'
        '"Instance received for shutdown" instance=i1 project=default'
        '"Instance received for shutdown" instance=i2 project=default'
        '"Instance received for shutdown" instance=i3 project=default'
        '"Instance received for shutdown" instance=i4 project=default'
        '"Instance received for shutdown" instance=i5 project=default'
        '"Instance shutdown complete" instance=i1 project=default'
        '"Instance shutdown complete" instance=i2 project=default'
        '"Instance shutdown complete" instance=i3 project=default'
        '"Instance shutdown complete" instance=i4 project=default'
        '"Instance shutdown complete" instance=i5 project=default'
        'Stopping daemon storage volumes'
        'Daemon storage volumes unmounted'
        'Operations deleted from the database'
        'Closing the database'
    )
    if ! check_log_presence "$scenario_name.log" "${expected_msgs[@]}"; then
        echo "Failed to find expected messages in the log file."
        exit 1
    fi

    ordered_msgs=(
        '"Stopping instances" stopPriority=3'
        '"Instance received for shutdown" instance=i5 project=default'
        '"Instance shutdown complete" instance=i5 project=default'
        '"Stopping instances" stopPriority=2'
        '"Instance received for shutdown" instance=i3 project=default'
        '"Instance received for shutdown" instance=i4 project=default'
        '"Instance shutdown complete" instance=i4 project=default'
        '"Stopping instances" stopPriority=1'
        '"Instance received for shutdown" instance=i2 project=default'
        '"Instance shutdown complete" instance=i2 project=default'
        '"Stopping instances" stopPriority=0'
        '"Instance received for shutdown" instance=i1 project=default'
        '"Instance shutdown complete" instance=i1 project=default'
    )
    if ! check_log_order "$scenario_name.log" "${ordered_msgs[@]}"; then
        echo "Failed to find given messages in the right order in the log file."
        exit 1
    fi

    # Cleanup
    delete_instances 5
    rm "$scenario_name.log"

    # The following scenarios are only relevant for LXD with storage backend other than ceph.
    # Indeed, the following scenarios are set a volume for `storage.backups_volume` and a Ceph
    # volume is not supported for this configuration (Ceph volume can't be used on multiple nodes concurrently,
    # which fails the validation at `daemonStorageValidate`)
    if [ "$lxd_backend" = "ceph" ]; then
        return 0
    fi

    scenario_name="scenario6"
    echo "$scenario_name"
    echo "- LXD shutdown sequence with instances running."
    echo "- Pending operations on some of them."
    echo "- Among the busy instances, they have different boot priorities."
    echo "- We also have the backup storage volume being used by an ongoing operation."
    echo "Expected behavior: LXD should shutdown without any issues."
    echo "----------------------------------------------------------------"

    lxc storage create backups "$lxd_backend"
    lxc storage volume create backups backups_volume

    lxc config set storage.backups_volume=backups/backups_volume
    if ! create_instances 5; then
        echo "Failed to create instances."
        exit 1
    fi

    lxc config set i1 boot.stop.priority 0
    lxc config set i2 boot.stop.priority 1
    lxc config set i3 boot.stop.priority 2
    lxc config set i4 boot.stop.priority 2
    lxc config set i5 boot.stop.priority 3

    lxc config set core.shutdown_timeout=1

    declare -A instance_ops_duration
    instance_ops_duration["i1"]=5s
    instance_ops_duration["i3"]=8s
    instance_ops_duration["i4"]=8s
    instance_ops_duration["i5"]=12s

    for instance_name in "${!instance_ops_duration[@]}"; do
        duration_seconds="${instance_ops_duration[$instance_name]}"
        echo "Starting operation for instance $instance_name for $duration_seconds seconds"
        lxd_websocket_operation "$instance_name" "$duration_seconds" &
    done

    # Simulate a volume operation that runs for 10 seconds.
    lxd_volume_operation backups backups_volume 10s &

    sleep 1
    lxd_shutdown_restart "$scenario_name" "$LXD_DIR"

    expected_msgs=(
        'Starting shutdown sequence'
        '"Unmounting daemon storage volumes"'
        '"Daemon storage volumes unmounted"'
        '"Stopping instances" stopPriority=3'
        '"Stopping instances" stopPriority=2'
        '"Stopping instances" stopPriority=1'
        '"Stopping instances" stopPriority=0'
        '"Instance received for shutdown" instance=i1 project=default'
        '"Instance received for shutdown" instance=i2 project=default'
        '"Instance received for shutdown" instance=i3 project=default'
        '"Instance received for shutdown" instance=i4 project=default'
        '"Instance received for shutdown" instance=i5 project=default'
        '"Instance shutdown complete" instance=i1 project=default'
        '"Instance shutdown complete" instance=i2 project=default'
        '"Instance shutdown complete" instance=i3 project=default'
        '"Instance shutdown complete" instance=i4 project=default'
        '"Instance shutdown complete" instance=i5 project=default'
        'Stopping daemon storage volumes'
        'Daemon storage volumes unmounted'
        'Operations deleted from the database'
        'Closing the database'
    )
    if ! check_log_presence "$scenario_name.log" "${expected_msgs[@]}"; then
        echo "Failed to find expected messages in the log file."
        exit 1
    fi

    ordered_msgs=(
        '"Stopping instances" stopPriority=3'
        '"Instance received for shutdown" instance=i5 project=default'
        '"Instance shutdown complete" instance=i5 project=default'
        '"Stopping instances" stopPriority=2'
        '"Instance received for shutdown" instance=i3 project=default'
        '"Instance received for shutdown" instance=i4 project=default'
        '"Instance shutdown complete" instance=i4 project=default'
        '"Stopping instances" stopPriority=1'
        '"Instance received for shutdown" instance=i2 project=default'
        '"Instance shutdown complete" instance=i2 project=default'
        '"Stopping instances" stopPriority=0'
        '"Instance received for shutdown" instance=i1 project=default'
        '"Instance shutdown complete" instance=i1 project=default'
    )
    if ! check_log_order "$scenario_name.log" "${ordered_msgs[@]}"; then
        echo "Failed to find given messages in the right order in the log file."
        exit 1
    fi

    # Cleanup
    delete_instances 5
    rm "$scenario_name.log"
    lxc config unset storage.backups_volume
    lxc storage volume delete backups backups_volume
    lxc storage delete backups

    scenario_name="scenario7"
    echo "$scenario_name"
    echo "- LXD shutdown sequence with instances running."
    echo "- Pending operations on some of them."
    echo "- Among the busy instances, they have different boot priorities."
    echo "- We also have the image storage volume being used by an ongoing operation."
    echo "Expected behavior: LXD should shutdown without any issues."
    echo "----------------------------------------------------------------"

    lxc storage create images "$lxd_backend"
    lxc storage volume create images images_volume

    lxc config set storage.images_volume=images/images_volume
    if ! create_instances 5; then
        echo "Failed to create instances."
        exit 1
    fi

    lxc config set i1 boot.stop.priority 0
    lxc config set i2 boot.stop.priority 1
    lxc config set i3 boot.stop.priority 2
    lxc config set i4 boot.stop.priority 2
    lxc config set i5 boot.stop.priority 3

    lxc config set core.shutdown_timeout=1

    declare -A instance_ops_duration
    instance_ops_duration["i1"]=5s
    instance_ops_duration["i3"]=8s
    instance_ops_duration["i4"]=8s
    instance_ops_duration["i5"]=12s

    for instance_name in "${!instance_ops_duration[@]}"; do
        duration_seconds="${instance_ops_duration[$instance_name]}"
        echo "Starting operation for instance $instance_name for $duration_seconds seconds"
        lxd_websocket_operation "$instance_name" "$duration_seconds" &
    done

    # Simulate a volume operation that runs for 10 seconds.
    lxd_volume_operation images images_volume 10s &

    sleep 1
    lxd_shutdown_restart "$scenario_name" "$LXD_DIR"

    expected_msgs=(
        'Starting shutdown sequence'
        '"Unmounting daemon storage volumes"'
        '"Daemon storage volumes unmounted"'
        '"Stopping instances" stopPriority=3'
        '"Stopping instances" stopPriority=2'
        '"Stopping instances" stopPriority=1'
        '"Stopping instances" stopPriority=0'
        '"Instance received for shutdown" instance=i1 project=default'
        '"Instance received for shutdown" instance=i2 project=default'
        '"Instance received for shutdown" instance=i3 project=default'
        '"Instance received for shutdown" instance=i4 project=default'
        '"Instance received for shutdown" instance=i5 project=default'
        '"Instance shutdown complete" instance=i1 project=default'
        '"Instance shutdown complete" instance=i2 project=default'
        '"Instance shutdown complete" instance=i3 project=default'
        '"Instance shutdown complete" instance=i4 project=default'
        '"Instance shutdown complete" instance=i5 project=default'
        'Stopping daemon storage volumes'
        'Daemon storage volumes unmounted'
        'Operations deleted from the database'
        'Closing the database'
    )
    if ! check_log_presence "$scenario_name.log" "${expected_msgs[@]}"; then
        echo "Failed to find expected messages in the log file."
        exit 1
    fi

    ordered_msgs=(
        '"Stopping instances" stopPriority=3'
        '"Instance received for shutdown" instance=i5 project=default'
        '"Instance shutdown complete" instance=i5 project=default'
        '"Stopping instances" stopPriority=2'
        '"Instance received for shutdown" instance=i3 project=default'
        '"Instance received for shutdown" instance=i4 project=default'
        '"Instance shutdown complete" instance=i4 project=default'
        '"Stopping instances" stopPriority=1'
        '"Instance received for shutdown" instance=i2 project=default'
        '"Instance shutdown complete" instance=i2 project=default'
        '"Stopping instances" stopPriority=0'
        '"Instance received for shutdown" instance=i1 project=default'
        '"Instance shutdown complete" instance=i1 project=default'
    )
    if ! check_log_order "$scenario_name.log" "${ordered_msgs[@]}"; then
        echo "Failed to find given messages in the right order in the log file."
        exit 1
    fi

    # Cleanup
    delete_instances 5
    rm "$scenario_name.log"
    lxc config unset storage.images_volume
    lxc storage volume delete images images_volume
    lxc storage delete images

    scenario_name="scenario8"
    echo "$scenario_name"
    echo "- LXD shutdown sequence with instances running."
    echo "- Pending operations on some of them."
    echo "- Among the busy instances, they have different boot priorities."
    echo "- We also have the image storage volume and backup storage volume being used by an ongoing operation."
    echo "Expected behavior: LXD should shutdown without any issues."
    echo "----------------------------------------------------------------"

    lxc storage create mypool "$lxd_backend"
    lxc storage volume create mypool backups_volume
    lxc storage volume create mypool images_volume

    lxc config set storage.images_volume=mypool/images_volume
    lxc config set storage.backups_volume=mypool/backups_volume

    if ! create_instances 5; then
        echo "Failed to create instances."
        exit 1
    fi

    lxc config set i1 boot.stop.priority 0
    lxc config set i2 boot.stop.priority 1
    lxc config set i3 boot.stop.priority 2
    lxc config set i4 boot.stop.priority 2
    lxc config set i5 boot.stop.priority 3

    lxc config set core.shutdown_timeout=1

    declare -A instance_ops_duration
    instance_ops_duration["i1"]=5s
    instance_ops_duration["i3"]=8s
    instance_ops_duration["i4"]=8s
    instance_ops_duration["i5"]=12s

    for instance_name in "${!instance_ops_duration[@]}"; do
        duration_seconds="${instance_ops_duration[$instance_name]}"
        echo "Starting operation for instance $instance_name for $duration_seconds seconds"
        lxd_websocket_operation "$instance_name" "$duration_seconds" &
    done

    # Simulate a volume operation on the images volume that runs for 10 seconds and on the backups volume that runs for 20 seconds.
    lxd_volume_operation mypool images_volume 5s &
    lxd_volume_operation mypool backups_volume 8s &

    sleep 1
    lxd_shutdown_restart "$scenario_name" "$LXD_DIR"

    expected_msgs=(
        'Starting shutdown sequence'
        '"Unmounting daemon storage volumes"'
        '"Daemon storage volumes unmounted"'
        '"Stopping instances" stopPriority=3'
        '"Stopping instances" stopPriority=2'
        '"Stopping instances" stopPriority=1'
        '"Stopping instances" stopPriority=0'
        '"Instance received for shutdown" instance=i1 project=default'
        '"Instance received for shutdown" instance=i2 project=default'
        '"Instance received for shutdown" instance=i3 project=default'
        '"Instance received for shutdown" instance=i4 project=default'
        '"Instance received for shutdown" instance=i5 project=default'
        '"Instance shutdown complete" instance=i1 project=default'
        '"Instance shutdown complete" instance=i2 project=default'
        '"Instance shutdown complete" instance=i3 project=default'
        '"Instance shutdown complete" instance=i4 project=default'
        '"Instance shutdown complete" instance=i5 project=default'
        'Stopping daemon storage volumes'
        'Daemon storage volumes unmounted'
        'Operations deleted from the database'
        'Closing the database'
    )
    if ! check_log_presence "$scenario_name.log" "${expected_msgs[@]}"; then
        echo "Failed to find expected messages in the log file."
        exit 1
    fi

    ordered_msgs=(
        '"Stopping instances" stopPriority=3'
        '"Instance received for shutdown" instance=i5 project=default'
        '"Instance shutdown complete" instance=i5 project=default'
        '"Stopping instances" stopPriority=2'
        '"Instance received for shutdown" instance=i3 project=default'
        '"Instance received for shutdown" instance=i4 project=default'
        '"Instance shutdown complete" instance=i4 project=default'
        '"Stopping instances" stopPriority=1'
        '"Instance received for shutdown" instance=i2 project=default'
        '"Instance shutdown complete" instance=i2 project=default'
        '"Stopping instances" stopPriority=0'
        '"Instance received for shutdown" instance=i1 project=default'
        '"Instance shutdown complete" instance=i1 project=default'
    )
    if ! check_log_order "$scenario_name.log" "${ordered_msgs[@]}"; then
        echo "Failed to find given messages in the right order in the log file."
        exit 1
    fi

    # Cleanup
    delete_instances 5
    rm "$scenario_name.log"
    lxc config unset storage.backups_volume
    lxc config unset storage.images_volume
    lxc storage volume delete mypool backups_volume
    lxc storage volume delete mypool images_volume
    lxc storage delete mypool

    scenario_name="scenario9"
    echo "$scenario_name"
    echo "- LXD shutdown sequence with instances running."
    echo "- Pending operations on some of them."
    echo "- Among the busy instances, they have different boot priorities."
    echo "- We also have the image and backup storage volume being used."
    echo "- The operations don't finish before the shutdown timeout is reached."
    echo "  * Among these operations that don't finish, it could be a shell session that remain open for example."
    echo "  * We should trigger the force shutdown of the instances."
    echo "  * Also, we could have a volume operation that is very long and observe the timeout as well."
    echo "Expected behavior: LXD should shutdown without any issues."
    echo "---------------------------------------------------------------------"

    lxc storage create mypool "$lxd_backend"
    lxc storage volume create mypool backups_volume
    lxc storage volume create mypool images_volume

    lxc config set storage.images_volume=mypool/images_volume
    lxc config set storage.backups_volume=mypool/backups_volume

    if ! create_instances 5; then
        echo "Failed to create instances."
        exit 1
    fi

    lxc config set i1 boot.stop.priority 0
    lxc config set i2 boot.stop.priority 1
    lxc config set i3 boot.stop.priority 2
    lxc config set i4 boot.stop.priority 2
    lxc config set i5 boot.stop.priority 3

    lxc config set core.shutdown_timeout=1

    declare -A instance_ops_duration
    instance_ops_duration["i1"]=80s # This operation will not finish before the shutdown timeout is reached. It will be force stopped.
    instance_ops_duration["i3"]=80s # Same as above.
    instance_ops_duration["i4"]=5s
    instance_ops_duration["i5"]=10s

    for instance_name in "${!instance_ops_duration[@]}"; do
        duration_seconds="${instance_ops_duration[$instance_name]}"
        echo "Starting operation for instance $instance_name for $duration_seconds seconds"
        lxd_websocket_operation "$instance_name" "$duration_seconds" &
    done

    # Simulate a volume operation on the images volume that runs for 10 seconds and on the backups volume that runs for 20 seconds.
    lxd_volume_operation mypool images_volume 5s &
    # This operation will not finish before the shutdown timeout is reached. An error log message should be shown.
    # In this situation, this is the unmount timeout that will be fired (1 minute and not the global shutdown timeout which is set to 2 minutes in this scenario).
    lxd_volume_operation mypool backups_volume 80s &

    sleep 1
    lxd_shutdown_restart "$scenario_name" "$LXD_DIR"

    expected_msgs=(
        'Starting shutdown sequence'
        '"Stopping instances" stopPriority=3'
        '"Stopping instances" stopPriority=2'
        '"Instance received for shutdown" instance=i4 project=default'
        '"Instance received for shutdown" instance=i5 project=default'
        '"Instance shutdown complete" instance=i4 project=default'
        '"Instance shutdown complete" instance=i5 project=default'
    )
    if ! check_log_presence "$scenario_name.log" "${expected_msgs[@]}"; then
        echo "Failed to find expected messages in the log file."
        exit 1
    fi

    ordered_msgs=(
        '"Stopping instances" stopPriority=3'
        '"Instance received for shutdown" instance=i5 project=default'
        '"Instance shutdown complete" instance=i5 project=default'
        '"Stopping instances" stopPriority=2'
        '"Instance received for shutdown" instance=i4 project=default'
        '"Instance shutdown complete" instance=i4 project=default'
    )
    if ! check_log_order "$scenario_name.log" "${ordered_msgs[@]}"; then
        echo "Failed to find given messages in the right order in the log file."
        exit 1
    fi

    # Cleanup
    delete_instances 5
    rm "$scenario_name.log"
    lxc config unset storage.backups_volume
    lxc config unset storage.images_volume
    lxc storage volume delete mypool backups_volume
    lxc storage volume delete mypool images_volume
    lxc storage delete mypool
}
