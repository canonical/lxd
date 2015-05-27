create_vg() {

    pvfile=$LXD_DIR/lvm-pv.img
    truncate -s 10G $pvfile
    pvloopdev=$(losetup -f)
    losetup $pvloopdev $pvfile

    #vgcreate will create a PV for us
    vgcreate lxd_test_vg $pvloopdev

}

cleanup_vg() {

    if [ -n "$LXD_INSPECT_LVM" ]; then
        echo "To poke around, use:\n LXD_DIR=$LXD_DIR sudo -E $GOPATH/bin/lxc COMMAND --config ${LXD_CONF} "
        read -p "Pausing to inspect LVM state. Hit Enter to continue cleanup." x
    fi

    if [ -d "$LXD_DIR"/lxc/testcontainer ]; then
        echo "unmounting testcontainer LV"
        umount "$LXD_DIR"/lxc/testcontainer
    fi

    # -f removes any LVs in the VG
    vgremove -f lxd_test_vg
    losetup -d $pvloopdev
    rm $pvfile

    cleanup
}

die() {
    set +x
    message=$1
    echo ""
    echo "\033[1;31m###### Test Failed : $message\033[0m"
    exit 1
}

test_lvm() {
    create_vg
    trap cleanup_vg EXIT HUP INT TERM

    test_lvm_withpool
    lvremove -f lxd_test_vg/LXDPool

    lvcreate -l 100%FREE --poolmetadatasize 500M --thinpool lxd_test_vg/test_user_thinpool
    test_lvm_withpool test_user_thinpool
}

test_lvm_withpool() {
    poolname=$1
    PREV_LXD_DIR=$LXD_DIR
    export LXD_DIR=$(mktemp -d -p $(pwd))
    chmod 777 "${LXD_DIR}"
    spawn_lxd 127.0.0.1:18451 "${LXD_DIR}"

    lxc config set core.lvm_vg_name "zambonirodeo" && die "Shouldn't be able to set nonexistent LVM VG"
    lxc config show | grep "core.lvm_vg_name" && die "vg_name should not be set after invalid attempt"

    lxc config set core.lvm_vg_name "lxd_test_vg" || die "error setting core.lvm_vg_name config"
    lxc config show | grep "lxd_test_vg" || die "test_vg not in config show output"

    if [ -n "$poolname" ]; then
        echo " --> Testing with user-supplied thin pool name '$poolname'"
        lxc config set core.lvm_thinpool_name $poolname || die "error setting core.lvm_thinpool_name config"
        lxc config show | grep "$poolname" || die "thin pool name not in config show output."
    else
        echo " --> Testing with default thin pool name 'LXDPool'"
        poolname=LXDPool
    fi

    ../scripts/lxd-images import busybox --alias testimage

    # get sha of image
    lxc image info testimage || die "Couldn't find testimage in lxc image info"
    testimage_sha=$(lxc image info testimage | grep Fingerprint | cut -d' ' -f 2)

    imagelvname=$testimage_sha

    lvs --noheadings -o lv_attr lxd_test_vg/$poolname | grep "^  t" || die "$poolname not found or not a thin pool"

    lvs --noheadings -o lv_attr lxd_test_vg/$imagelvname | grep "^  V" || die "no lv named $imagelvname found or not a thin Vol."

    lvs --noheadings -o pool_lv lxd_test_vg/$imagelvname | grep "$poolname" || die "new LV not member of $poolname"

    # launch a container using that image

    lxc init testimage testcontainer || die "Couldn't init test container"

    # check that we now have a new volume in the pool
    lvs --noheadings -o pool_lv lxd_test_vg/testcontainer | grep "$poolname" || die "LV for new container not found or not in $poolname"

    lxc start testcontainer || die "Couldn't start testcontainer"
    lxc list testcontainer | grep RUNNING || die "testcontainer doesn't seem to be running"
    lxc stop testcontainer --force || die "Couldn't stop testcontainer"

    lxc delete testcontainer || die "Couldn't delete testcontainer"
    lvs lxd_test_vg/testcontainer && die "testcontainer LV is still there, should've been destroyed"
    lxc image delete testimage || die "Couldn't delete testimage"

    lvs lxd_test_vg/$imagelvname && die "lv $imagelvname is still there, should be gone"

    kill -9 `cat $LXD_DIR/lxd.pid`
    sleep 3
    rm -Rf ${LXD_DIR}
    LXD_DIR=${PREV_LXD_DIR}
}
