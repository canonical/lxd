s3cmdrun () {
  local backend accessKey secreyKey
  backend="${1}"
  accessKey="${2}"
  secreyKey="${3}"
  shift 3

  if [ "$backend" = "ceph" ]; then
    timeout -k 5 5 s3cmd \
      --access_key="${accessKey}" \
      --secret_key="${secreyKey}" \
      --host="${s3Endpoint}" \
      --host-bucket="${s3Endpoint}" \
      --no-ssl \
      "$@"
  else
    timeout -k 5 5 s3cmd \
      --access_key="${accessKey}" \
      --secret_key="${secreyKey}" \
      --host="${s3Endpoint}" \
      --host-bucket="${s3Endpoint}" \
      --ssl \
      --no-check-certificate \
      "$@"
  fi
}

test_storage_buckets() {
  local lxd_backend

  lxd_backend=$(storage_backend "$LXD_DIR")

  if [ "$lxd_backend" = "ceph" ]; then
    if [ -z "${LXD_CEPH_CEPHOBJECT_RADOSGW:-}" ]; then
      # Check LXD_CEPH_CEPHOBJECT_RADOSGW specified for ceph bucket tests.
      export TEST_UNMET_REQUIREMENT="LXD_CEPH_CEPHOBJECT_RADOSGW not specified"
      return
    fi
  elif ! command -v minio ; then
    # Check minio is installed for local storage pool buckets.
    export TEST_UNMET_REQUIREMENT="minio command not found"
    return
  fi

  poolName="s3"
  bucketPrefix="lxd$$"

  create_object_storage_pool "${poolName}"

  # Check cephobject.radosgw.endpoint is required for cephobject pools.
  if [ "$lxd_backend" = "ceph" ]; then
    s3Endpoint="${LXD_CEPH_CEPHOBJECT_RADOSGW}"
  else
    s3Endpoint="https://$(lxc config get core.storage_buckets_address)"
  fi

  # Check bucket name validation.
  ! lxc storage bucket create "${poolName}" .foo || false
  ! lxc storage bucket create "${poolName}" fo || false
  ! lxc storage bucket create "${poolName}" fooooooooooooooooooooooooooooooooooooooooooooooooooooooooooooooo || false
  ! lxc storage bucket create "${poolName}" "foo bar" || false

  # Create bucket.
  initCreds=$(lxc storage bucket create "${poolName}" "${bucketPrefix}.foo" user.foo=comment)
  initAccessKey=$(echo "${initCreds}" | awk '{ if ($2 == "access" && $3 == "key:") {print $4}}')
  initSecretKey=$(echo "${initCreds}" | awk '{ if ($2 == "secret" && $3 == "key:") {print $4}}')
  s3cmdrun "${lxd_backend}" "${initAccessKey}" "${initSecretKey}" ls | grep -F "${bucketPrefix}.foo"

  # Check if the storage bucket has an UUID.
  [ -n "$(lxc storage bucket get "${poolName}" "${bucketPrefix}.foo" volatile.uuid)" ]

  lxc storage bucket list "${poolName}" | grep -F "${bucketPrefix}.foo"
  lxc storage bucket show "${poolName}" "${bucketPrefix}.foo"

  # Create bucket keys.

  # Create admin key with randomly generated credentials.
  creds=$(lxc storage bucket key create "${poolName}" "${bucketPrefix}.foo" admin-key --role=admin)
  adAccessKey=$(echo "${creds}" | awk '{ if ($1 == "Access" && $2 == "key:") {print $3}}')
  adSecretKey=$(echo "${creds}" | awk '{ if ($1 == "Secret" && $2 == "key:") {print $3}}')

  # Create read-only key with manually specified credentials.
  creds=$(lxc storage bucket key create "${poolName}" "${bucketPrefix}.foo" ro-key --role=read-only --access-key="${bucketPrefix}.foo.ro" --secret-key="password")
  roAccessKey=$(echo "${creds}" | awk '{ if ($1 == "Access" && $2 == "key:") {print $3}}')
  roSecretKey=$(echo "${creds}" | awk '{ if ($1 == "Secret" && $2 == "key:") {print $3}}')

  # Test creating a bucket key with YAML bucket key config.
  creds=$(lxc storage bucket key create "${poolName}" "${bucketPrefix}.foo" yaml-key << EOF
description: yaml-key-desc
role: read-only
EOF
)
  roYamlAccessKey=$(echo "${creds}" | awk '{ if ($1 == "Access" && $2 == "key:") {print $3}}')
  roYamlSecretKey=$(echo "${creds}" | awk '{ if ($1 == "Secret" && $2 == "key:") {print $3}}')

  lxc storage bucket key list "${poolName}" "${bucketPrefix}.foo" | grep -F "admin-key"
  lxc storage bucket key list "${poolName}" "${bucketPrefix}.foo" | grep -F "ro-key"
  lxc storage bucket key show "${poolName}" "${bucketPrefix}.foo" admin-key
  lxc storage bucket key show "${poolName}" "${bucketPrefix}.foo" ro-key
  lxc storage bucket key show "${poolName}" "${bucketPrefix}.foo" yaml-key | grep "description: yaml-key-desc"

  # Test listing buckets via S3.
  s3cmdrun "${lxd_backend}" "${adAccessKey}" "${adSecretKey}" ls | grep -F "${bucketPrefix}.foo"
  s3cmdrun "${lxd_backend}" "${roAccessKey}" "${roSecretKey}" ls | grep -F "${bucketPrefix}.foo"
  s3cmdrun "${lxd_backend}" "${roYamlAccessKey}" "${roYamlSecretKey}" ls | grep -F "${bucketPrefix}.foo"

  # Test making buckets via S3 is blocked.
  ! s3cmdrun "${lxd_backend}" "${adAccessKey}" "${adSecretKey}" mb "s3://${bucketPrefix}.foo2" || false
  ! s3cmdrun "${lxd_backend}" "${roAccessKey}" "${roSecretKey}" mb "s3://${bucketPrefix}.foo2" || false

  # Test putting a file into a bucket.
  lxdTestFile="bucketfile_${bucketPrefix}.txt"
  head -c 5M /dev/urandom > "${lxdTestFile}"
  ORIG_MD5SUM="$(md5sum < "${lxdTestFile}")"
  s3cmdrun "${lxd_backend}" "${adAccessKey}" "${adSecretKey}" put "${lxdTestFile}" "s3://${bucketPrefix}.foo"
  ! s3cmdrun "${lxd_backend}" "${roAccessKey}" "${roSecretKey}" put "${lxdTestFile}" "s3://${bucketPrefix}.foo" || false

  # Test listing bucket files via S3.
  s3cmdrun "${lxd_backend}" "${adAccessKey}" "${adSecretKey}" ls "s3://${bucketPrefix}.foo" | grep -F "${lxdTestFile}"
  s3cmdrun "${lxd_backend}" "${roAccessKey}" "${roSecretKey}" ls "s3://${bucketPrefix}.foo" | grep -F "${lxdTestFile}"

  # Test getting a file from a bucket.
  INFO_MD5SUM="$(s3cmdrun "${lxd_backend}" "${adAccessKey}" "${adSecretKey}" info "s3://${bucketPrefix}.foo/${lxdTestFile}" | awk '{ if ($1 == "MD5") {print $3}}')  -"
  s3cmdrun "${lxd_backend}" "${adAccessKey}" "${adSecretKey}" get "s3://${bucketPrefix}.foo/${lxdTestFile}" "${lxdTestFile}.get"
  [ "${ORIG_MD5SUM}" = "${INFO_MD5SUM}" ]
  [ "${ORIG_MD5SUM}" = "$(md5sum < "${lxdTestFile}.get")" ]
  rm "${lxdTestFile}.get"
  INFO_MD5SUM="$(s3cmdrun "${lxd_backend}" "${roAccessKey}" "${roSecretKey}" info "s3://${bucketPrefix}.foo/${lxdTestFile}" | awk '{ if ($1 == "MD5") {print $3}}')  -"
  s3cmdrun "${lxd_backend}" "${roAccessKey}" "${roSecretKey}" get "s3://${bucketPrefix}.foo/${lxdTestFile}" "${lxdTestFile}.get"
  [ "${ORIG_MD5SUM}" = "${INFO_MD5SUM}" ]
  [ "${ORIG_MD5SUM}" = "$(md5sum < "${lxdTestFile}.get")" ]
  rm "${lxdTestFile}.get"

  # Test setting bucket policy to allow anonymous access (also tests bucket URL generation).
  bucketURL=$(lxc storage bucket show "${poolName}" "${bucketPrefix}.foo" | awk '{if ($1 == "s3_url:") {print $2}}')

  [ "$(curl -sI --insecure -o /dev/null -w "%{http_code}" "${bucketURL}/${lxdTestFile}")" = "403" ]
  ! s3cmdrun "${lxd_backend}" "${roAccessKey}" "${roSecretKey}" setpolicy deps/s3_global_read_policy.json "s3://${bucketPrefix}.foo" || false
  s3cmdrun "${lxd_backend}" "${adAccessKey}" "${adSecretKey}" setpolicy deps/s3_global_read_policy.json "s3://${bucketPrefix}.foo"
  [ "$(curl -sI --insecure -o /dev/null -w "%{http_code}" "${bucketURL}/${lxdTestFile}")" = "200" ]
  ! s3cmdrun "${lxd_backend}" "${roAccessKey}" "${roSecretKey}" delpolicy "s3://${bucketPrefix}.foo" || false
  s3cmdrun "${lxd_backend}" "${adAccessKey}" "${adSecretKey}" delpolicy "s3://${bucketPrefix}.foo"
  [ "$(curl -sI --insecure -o /dev/null -w "%{http_code}" "${bucketURL}/${lxdTestFile}")" = "403" ]

  # Test deleting a file from a bucket.
  ! s3cmdrun "${lxd_backend}" "${roAccessKey}" "${roSecretKey}" del "s3://${bucketPrefix}.foo/${lxdTestFile}" || false
  s3cmdrun "${lxd_backend}" "${adAccessKey}" "${adSecretKey}" del "s3://${bucketPrefix}.foo/${lxdTestFile}"

  # Test bucket quota (except dir driver which doesn't support quotas so check that its prevented).
  if [ "$lxd_backend" = "dir" ]; then
    ! lxc storage bucket create "${poolName}" "${bucketPrefix}.foo2" size=1MiB || false
  else
    initCreds=$(lxc storage bucket create "${poolName}" "${bucketPrefix}.foo2" size=1MiB)
    initAccessKey=$(echo "${initCreds}" | awk '{ if ($2 == "access" && $3 == "key:") {print $4}}')
    initSecretKey=$(echo "${initCreds}" | awk '{ if ($2 == "secret" && $3 == "key:") {print $4}}')
    ! s3cmdrun "${lxd_backend}" "${initAccessKey}" "${initSecretKey}" put "${lxdTestFile}" "s3://${bucketPrefix}.foo2" || false

    # Grow bucket quota (significantly larger in order for MinIO to detect their is sufficient space to continue).
    lxc storage bucket set "${poolName}" "${bucketPrefix}.foo2" size=150MiB
    s3cmdrun "${lxd_backend}" "${initAccessKey}" "${initSecretKey}" put "${lxdTestFile}" "s3://${bucketPrefix}.foo2"
    s3cmdrun "${lxd_backend}" "${initAccessKey}" "${initSecretKey}" del "s3://${bucketPrefix}.foo2/${lxdTestFile}"
    lxc storage bucket delete "${poolName}" "${bucketPrefix}.foo2"
  fi

  # Cleanup test file used earlier.
  rm "${lxdTestFile}"

  # Test deleting bucket via s3.
  ! s3cmdrun "${lxd_backend}" "${roAccessKey}" "${roSecretKey}" rb "s3://${bucketPrefix}.foo" || false
  s3cmdrun "${lxd_backend}" "${adAccessKey}" "${adSecretKey}" rb "s3://${bucketPrefix}.foo"

  # Delete bucket keys.
  lxc storage bucket key delete "${poolName}" "${bucketPrefix}.foo" admin-key
  lxc storage bucket key delete "${poolName}" "${bucketPrefix}.foo" ro-key
  ! lxc storage bucket key list "${poolName}" "${bucketPrefix}.foo" | grep -F "admin-key" || false
  ! lxc storage bucket key list "${poolName}" "${bucketPrefix}.foo" | grep -F "ro-key" || false
  ! lxc storage bucket key show "${poolName}" "${bucketPrefix}.foo" admin-key || false
  ! lxc storage bucket key show "${poolName}" "${bucketPrefix}.foo" ro-key || false
  ! s3cmdrun "${lxd_backend}" "${adAccessKey}" "${adSecretKey}" ls || false
  ! s3cmdrun "${lxd_backend}" "${roAccessKey}" "${roSecretKey}" ls || false

  # Delete bucket.
  lxc storage bucket delete "${poolName}" "${bucketPrefix}.foo"
  ! lxc storage bucket list "${poolName}" | grep -F "${bucketPrefix}.foo" || false
  ! lxc storage bucket show "${poolName}" "${bucketPrefix}.foo" || false

  delete_object_storage_pool "${poolName}"
}

test_storage_bucket_export() {
  # shellcheck disable=2039,3043
  local lxd_backend

  lxd_backend=$(storage_backend "$LXD_DIR")

  # Skip the test if we are not using a Ceph or Dir backend, because the test requires a storage pool
  # larger than 1 GiB due to the Minio requirements: https://github.com/minio/minio/issues/6795
  if [ ! "$lxd_backend" = "ceph" ] && [ ! "$lxd_backend" = "dir" ]; then
    return
  fi

  if [ "$lxd_backend" = "ceph" ]; then
    if [ -z "${LXD_CEPH_CEPHOBJECT_RADOSGW:-}" ]; then
      # Check LXD_CEPH_CEPHOBJECT_RADOSGW specified for ceph bucket tests.
      export TEST_UNMET_REQUIREMENT="LXD_CEPH_CEPHOBJECT_RADOSGW not specified"
      return
    fi
  elif ! command -v minio ; then
    # Check minio is installed for local storage pool buckets.
    export TEST_UNMET_REQUIREMENT="minio command not found"
    return
  fi

  poolName=$(lxc profile device get default root pool)
  bucketPrefix="lxd$$"

  # Check cephobject.radosgw.endpoint is required for cephobject pools.
  if [ "$lxd_backend" = "ceph" ]; then
    ! lxc storage create s3 cephobject || false
    lxc storage create s3 cephobject cephobject.radosgw.endpoint="${LXD_CEPH_CEPHOBJECT_RADOSGW}"
    lxc storage show s3
    poolName="s3"
    s3Endpoint="${LXD_CEPH_CEPHOBJECT_RADOSGW}"
  else
    # Create a loop device for dir pools as MinIO doesn't support running on tmpfs (which the test suite can do).
    if [ "$lxd_backend" = "dir" ]; then
      configure_loop_device loop_file_1 loop_device_1
      # shellcheck disable=SC2154
      mkfs.ext4 "${loop_device_1}"
      mkdir "${TEST_DIR}/${bucketPrefix}"
      mount "${loop_device_1}" "${TEST_DIR}/${bucketPrefix}"
      losetup -d "${loop_device_1}"
      mkdir "${TEST_DIR}/${bucketPrefix}/s3"
      # shellcheck disable=SC2034
      lxc storage create s3 dir source="${TEST_DIR}/${bucketPrefix}/s3"
      poolName="s3"
    fi

    buckets_addr="127.0.0.1:$(local_tcp_port)"
    lxc config set core.storage_buckets_address "${buckets_addr}"
    s3Endpoint="https://${buckets_addr}"
  fi

  # Create test bucket
  initCreds=$(lxc storage bucket create "${poolName}" "${bucketPrefix}.foo" user.foo=comment)
  initAccessKey=$(echo "${initCreds}" | awk '{ if ($2 == "access" && $3 == "key:") {print $4}}')
  initSecretKey=$(echo "${initCreds}" | awk '{ if ($2 == "secret" && $3 == "key:") {print $4}}')
  s3cmdrun "${lxd_backend}" "${initAccessKey}" "${initSecretKey}" ls | grep -F "${bucketPrefix}.foo"

  # Test putting a file into a bucket.
  lxdTestFile="bucketfile_${bucketPrefix}.txt"
  echo "hello world"> "${lxdTestFile}"
  s3cmdrun "${lxd_backend}" "${initAccessKey}" "${initSecretKey}" put "${lxdTestFile}" "s3://${bucketPrefix}.foo"

  # Export test bucket
  lxc storage bucket export "${poolName}" "${bucketPrefix}.foo" "${LXD_DIR}/testbucket.tar.gz"
  [ -f "${LXD_DIR}/testbucket.tar.gz" ]

  # Extract storage backup tarball.
  mkdir "${LXD_DIR}/storage-bucket-export"
  tar -xzf "${LXD_DIR}/testbucket.tar.gz" -C "${LXD_DIR}/storage-bucket-export"

  # Check tarball content.
  [ -f "${LXD_DIR}/storage-bucket-export/backup/index.yaml" ]
  [ -f "${LXD_DIR}/storage-bucket-export/backup/bucket/${lxdTestFile}" ]
  [ "$(cat "${LXD_DIR}/storage-bucket-export/backup/bucket/${lxdTestFile}")" = "hello world" ]

  # Delete bucket and import exported bucket
  lxc storage bucket delete "${poolName}" "${bucketPrefix}.foo"
  lxc storage bucket import "${poolName}" "${LXD_DIR}/testbucket.tar.gz" "${bucketPrefix}.bar"
  rm "${LXD_DIR}/testbucket.tar.gz"

  # Test listing bucket files via S3.
  s3cmdrun "${lxd_backend}" "${initAccessKey}" "${initSecretKey}" ls "s3://${bucketPrefix}.bar" | grep -F "${lxdTestFile}"

  # Test getting admin key from bucket.
  lxc storage bucket key list "${poolName}" "${bucketPrefix}.bar" | grep -F "admin"

  # Clean up.
  lxc storage bucket delete "${poolName}" "${bucketPrefix}.bar"
  ! lxc storage bucket list "${poolName}" | grep -F "${bucketPrefix}.bar" || false
  ! lxc storage bucket show "${poolName}" "${bucketPrefix}.bar" || false

  if [ "$lxd_backend" = "ceph" ] || [ "$lxd_backend" = "dir" ]; then
    lxc storage delete "${poolName}"
  fi

  if [ "$lxd_backend" = "dir" ]; then
    umount "${TEST_DIR}/${bucketPrefix}"
    rmdir "${TEST_DIR}/${bucketPrefix}"

    # shellcheck disable=SC2154
    deconfigure_loop_device "${loop_file_1}" "${loop_device_1}"
  fi

  rm -rf "${LXD_DIR}/storage-bucket-export/"*
  rmdir "${LXD_DIR}/storage-bucket-export"
}
