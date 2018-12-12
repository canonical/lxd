test_security() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  # CVE-2016-1581
  if [ "$(storage_backend "$LXD_DIR")" = "zfs" ]; then
    LXD_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    chmod +x "${LXD_INIT_DIR}"
    spawn_lxd "${LXD_INIT_DIR}" false false

    ZFS_POOL="lxdtest-$(basename "${LXD_DIR}")-init"
    LXD_DIR=${LXD_INIT_DIR} lxd init --storage-backend zfs --storage-create-loop 1 --storage-pool "${ZFS_POOL}" --auto

    PERM=$(stat -c %a "${LXD_INIT_DIR}/disks/${ZFS_POOL}.img")
    if [ "${PERM}" != "600" ]; then
      echo "Bad zfs.img permissions: ${PERM}"
      false
    fi

    kill_lxd "${LXD_INIT_DIR}"
  fi

  # CVE-2016-1582
  lxc launch testimage test-priv -c security.privileged=true

  PERM=$(stat -L -c %a "${LXD_DIR}/containers/test-priv")
  if [ "${PERM}" != "700" ]; then
    echo "Bad container permissions: ${PERM}"
    false
  fi

  lxc config set test-priv security.privileged false
  lxc restart test-priv --force
  lxc config set test-priv security.privileged true
  lxc restart test-priv --force

  PERM=$(stat -L -c %a "${LXD_DIR}/containers/test-priv")
  if [ "${PERM}" != "700" ]; then
    echo "Bad container permissions: ${PERM}"
    false
  fi

  lxc delete test-priv --force

  lxc launch testimage test-unpriv
  lxc config set test-unpriv security.privileged true
  lxc restart test-unpriv --force

  PERM=$(stat -L -c %a "${LXD_DIR}/containers/test-unpriv")
  if [ "${PERM}" != "700" ]; then
    echo "Bad container permissions: ${PERM}"
    false
  fi

  lxc delete test-unpriv --force

  # shellcheck disable=2039
  local LXD_STORAGE_DIR

  LXD_STORAGE_DIR=$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)
  chmod +x "${LXD_STORAGE_DIR}"
  # Enforce that only unprivileged containers can be created
  LXD_UNPRIVILEGED_ONLY=true
  export LXD_UNPRIVILEGED_ONLY
  spawn_lxd "${LXD_STORAGE_DIR}" true true
  unset LXD_UNPRIVILEGED_ONLY

  (
    set -e
    # shellcheck disable=2030
    LXD_DIR="${LXD_STORAGE_DIR}"

    # Import image into default storage pool.
    ensure_import_testimage

    # Verify that no privileged container can be created
    ! lxc launch testimage c1 -c security.privileged=true

    # Verify that unprivileged container can be created
    lxc launch testimage c1

    # Verify that we can't be tricked into using privileged containers
    ! lxc config set c1 security.privileged true
    ! lxc config set c1 raw.idmap "both 1000 1000"
    ! lxc config set c1 raw.lxc "lxc.idmap="
    ! lxc config set c1 raw.lxc "lxc.include="

    # Verify that we can still unset and set to security.privileged to "false"
    lxc config set c1 security.privileged false
    lxc config unset c1 security.privileged

    # Verify that a profile can't be changed to trick us into using privileged
    # containers
    ! lxc profile set default security.privileged true
    ! lxc profile set default raw.idmap "both 1000 1000"
    ! lxc profile set default raw.lxc "lxc.idmap="
    ! lxc profile set default raw.lxc "lxc.include="

    # Verify that we can still unset and set to security.privileged to "false"
    lxc profile set default security.privileged false
    lxc profile unset default security.privileged

    lxc delete -f c1
  )

  # shellcheck disable=SC2031
  LXD_DIR="${LXD_DIR}"
  kill_lxd "${LXD_STORAGE_DIR}"
}

test_security_protection() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  # Test deletion protecton
  lxc init testimage c1
  lxc snapshot c1
  lxc delete c1

  lxc profile set default security.protection.delete true

  lxc init testimage c1
  lxc snapshot c1
  lxc delete c1/snap0
  ! lxc delete c1

  lxc config set c1 security.protection.delete false
  lxc delete c1

  lxc profile unset default security.protection.delete

  # Test shifting protection
  lxc init testimage c1
  lxc start c1
  lxc stop c1 --force

  lxc profile set default security.protection.shift true
  lxc start c1
  lxc stop c1 --force

  ! lxc publish c1 --alias=protected
  lxc snapshot c1
  lxc publish c1/snap0 --alias=protected
  lxc image delete protected

  lxc config set c1 security.privileged true
  ! lxc start c1
  lxc config set c1 security.protection.shift false
  lxc start c1
  lxc stop c1 --force

  lxc delete c1
  lxc profile unset default security.protection.shift
}
