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
  s3cmdrun "${s3Endpoint}" "${lxd_backend}" "${initAccessKey}" "${initSecretKey}" ls | grep -F "${bucketPrefix}.foo"

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
  s3cmdrun "${s3Endpoint}" "${lxd_backend}" "${adAccessKey}" "${adSecretKey}" ls | grep -F "${bucketPrefix}.foo"
  s3cmdrun "${s3Endpoint}" "${lxd_backend}" "${roAccessKey}" "${roSecretKey}" ls | grep -F "${bucketPrefix}.foo"
  s3cmdrun "${s3Endpoint}" "${lxd_backend}" "${roYamlAccessKey}" "${roYamlSecretKey}" ls | grep -F "${bucketPrefix}.foo"

  # Test making buckets via S3 is blocked.
  ! s3cmdrun "${s3Endpoint}" "${lxd_backend}" "${adAccessKey}" "${adSecretKey}" mb "s3://${bucketPrefix}.foo2" || false
  ! s3cmdrun "${s3Endpoint}" "${lxd_backend}" "${roAccessKey}" "${roSecretKey}" mb "s3://${bucketPrefix}.foo2" || false

  # Test putting a file into a bucket.
  lxdTestFile="bucketfile_${bucketPrefix}.txt"
  head -c 5M /dev/urandom > "${lxdTestFile}"
  ORIG_MD5SUM="$(md5sum < "${lxdTestFile}")"
  s3cmdrun "${s3Endpoint}" "${lxd_backend}" "${adAccessKey}" "${adSecretKey}" put "${lxdTestFile}" "s3://${bucketPrefix}.foo"
  ! s3cmdrun "${s3Endpoint}" "${lxd_backend}" "${roAccessKey}" "${roSecretKey}" put "${lxdTestFile}" "s3://${bucketPrefix}.foo" || false

  # Test listing bucket files via S3.
  s3cmdrun "${s3Endpoint}" "${lxd_backend}" "${adAccessKey}" "${adSecretKey}" ls "s3://${bucketPrefix}.foo" | grep -F "${lxdTestFile}"
  s3cmdrun "${s3Endpoint}" "${lxd_backend}" "${roAccessKey}" "${roSecretKey}" ls "s3://${bucketPrefix}.foo" | grep -F "${lxdTestFile}"

  # Test getting a file from a bucket.
  INFO_MD5SUM="$(s3cmdrun "${s3Endpoint}" "${lxd_backend}" "${adAccessKey}" "${adSecretKey}" info "s3://${bucketPrefix}.foo/${lxdTestFile}" | awk '{ if ($1 == "MD5") {print $3}}')  -"
  s3cmdrun "${s3Endpoint}" "${lxd_backend}" "${adAccessKey}" "${adSecretKey}" get "s3://${bucketPrefix}.foo/${lxdTestFile}" "${lxdTestFile}.get"
  [ "${ORIG_MD5SUM}" = "${INFO_MD5SUM}" ]
  [ "${ORIG_MD5SUM}" = "$(md5sum < "${lxdTestFile}.get")" ]
  rm "${lxdTestFile}.get"
  INFO_MD5SUM="$(s3cmdrun "${s3Endpoint}" "${lxd_backend}" "${roAccessKey}" "${roSecretKey}" info "s3://${bucketPrefix}.foo/${lxdTestFile}" | awk '{ if ($1 == "MD5") {print $3}}')  -"
  s3cmdrun "${s3Endpoint}" "${lxd_backend}" "${roAccessKey}" "${roSecretKey}" get "s3://${bucketPrefix}.foo/${lxdTestFile}" "${lxdTestFile}.get"
  [ "${ORIG_MD5SUM}" = "${INFO_MD5SUM}" ]
  [ "${ORIG_MD5SUM}" = "$(md5sum < "${lxdTestFile}.get")" ]
  rm "${lxdTestFile}.get"

  # Test setting bucket policy to allow anonymous access (also tests bucket URL generation).
  bucketURL=$(lxc storage bucket show "${poolName}" "${bucketPrefix}.foo" | awk '{if ($1 == "s3_url:") {print $2}}')

  [ "$(curl -sI --insecure -o /dev/null -w "%{http_code}" "${bucketURL}/${lxdTestFile}")" = "403" ]
  ! s3cmdrun "${s3Endpoint}" "${lxd_backend}" "${roAccessKey}" "${roSecretKey}" setpolicy deps/s3_global_read_policy.json "s3://${bucketPrefix}.foo" || false
  s3cmdrun "${s3Endpoint}" "${lxd_backend}" "${adAccessKey}" "${adSecretKey}" setpolicy deps/s3_global_read_policy.json "s3://${bucketPrefix}.foo"
  [ "$(curl -sI --insecure -o /dev/null -w "%{http_code}" "${bucketURL}/${lxdTestFile}")" = "200" ]
  ! s3cmdrun "${s3Endpoint}" "${lxd_backend}" "${roAccessKey}" "${roSecretKey}" delpolicy "s3://${bucketPrefix}.foo" || false
  s3cmdrun "${s3Endpoint}" "${lxd_backend}" "${adAccessKey}" "${adSecretKey}" delpolicy "s3://${bucketPrefix}.foo"
  [ "$(curl -sI --insecure -o /dev/null -w "%{http_code}" "${bucketURL}/${lxdTestFile}")" = "403" ]

  # Test deleting a file from a bucket.
  ! s3cmdrun "${s3Endpoint}" "${lxd_backend}" "${roAccessKey}" "${roSecretKey}" del "s3://${bucketPrefix}.foo/${lxdTestFile}" || false
  s3cmdrun "${s3Endpoint}" "${lxd_backend}" "${adAccessKey}" "${adSecretKey}" del "s3://${bucketPrefix}.foo/${lxdTestFile}"

  # Test bucket quota (except dir driver which doesn't support quotas so check that its prevented).
  if [ "$lxd_backend" = "dir" ]; then
    ! lxc storage bucket create "${poolName}" "${bucketPrefix}.foo2" size=1MiB || false
  else
    initCreds=$(lxc storage bucket create "${poolName}" "${bucketPrefix}.foo2" size=1MiB)
    initAccessKey=$(echo "${initCreds}" | awk '{ if ($2 == "access" && $3 == "key:") {print $4}}')
    initSecretKey=$(echo "${initCreds}" | awk '{ if ($2 == "secret" && $3 == "key:") {print $4}}')
    ! s3cmdrun "${s3Endpoint}" "${lxd_backend}" "${initAccessKey}" "${initSecretKey}" put "${lxdTestFile}" "s3://${bucketPrefix}.foo2" || false

    # Grow bucket quota (significantly larger in order for MinIO to detect their is sufficient space to continue).
    lxc storage bucket set "${poolName}" "${bucketPrefix}.foo2" size=150MiB
    s3cmdrun "${s3Endpoint}" "${lxd_backend}" "${initAccessKey}" "${initSecretKey}" put "${lxdTestFile}" "s3://${bucketPrefix}.foo2"
    s3cmdrun "${s3Endpoint}" "${lxd_backend}" "${initAccessKey}" "${initSecretKey}" del "s3://${bucketPrefix}.foo2/${lxdTestFile}"
    lxc storage bucket delete "${poolName}" "${bucketPrefix}.foo2"
  fi

  # Cleanup test file used earlier.
  rm "${lxdTestFile}"

  # Test deleting bucket via s3.
  ! s3cmdrun "${s3Endpoint}" "${lxd_backend}" "${roAccessKey}" "${roSecretKey}" rb "s3://${bucketPrefix}.foo" || false
  s3cmdrun "${s3Endpoint}" "${lxd_backend}" "${adAccessKey}" "${adSecretKey}" rb "s3://${bucketPrefix}.foo"

  # Delete bucket keys.
  lxc storage bucket key delete "${poolName}" "${bucketPrefix}.foo" admin-key
  lxc storage bucket key delete "${poolName}" "${bucketPrefix}.foo" ro-key
  ! lxc storage bucket key list "${poolName}" "${bucketPrefix}.foo" | grep -F "admin-key" || false
  ! lxc storage bucket key list "${poolName}" "${bucketPrefix}.foo" | grep -F "ro-key" || false
  ! lxc storage bucket key show "${poolName}" "${bucketPrefix}.foo" admin-key || false
  ! lxc storage bucket key show "${poolName}" "${bucketPrefix}.foo" ro-key || false
  ! s3cmdrun "${s3Endpoint}" "${lxd_backend}" "${adAccessKey}" "${adSecretKey}" ls || false
  ! s3cmdrun "${s3Endpoint}" "${lxd_backend}" "${roAccessKey}" "${roSecretKey}" ls || false

  # Delete bucket.
  lxc storage bucket delete "${poolName}" "${bucketPrefix}.foo"
  ! lxc storage bucket list "${poolName}" | grep -F "${bucketPrefix}.foo" || false
  ! lxc storage bucket show "${poolName}" "${bucketPrefix}.foo" || false

  delete_object_storage_pool "${poolName}"
}

test_storage_bucket_export() {
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

  poolName=$(lxc profile device get default root pool)
  bucketPrefix="lxd$$"

  create_object_storage_pool "${poolName}"

  # Check cephobject.radosgw.endpoint is required for cephobject pools.
  if [ "$lxd_backend" = "ceph" ]; then
    s3Endpoint="${LXD_CEPH_CEPHOBJECT_RADOSGW}"
  else
    s3Endpoint="https://$(lxc config get core.storage_buckets_address)"
  fi

  # Create test bucket
  initCreds=$(lxc storage bucket create "${poolName}" "${bucketPrefix}.foo" user.foo=comment)
  initAccessKey=$(echo "${initCreds}" | awk '{ if ($2 == "access" && $3 == "key:") {print $4}}')
  initSecretKey=$(echo "${initCreds}" | awk '{ if ($2 == "secret" && $3 == "key:") {print $4}}')
  s3cmdrun "${s3Endpoint}" "${lxd_backend}" "${initAccessKey}" "${initSecretKey}" ls | grep -F "${bucketPrefix}.foo"

  # Test putting a file into a bucket.
  lxdTestFile="bucketfile_${bucketPrefix}.txt"
  echo "hello world"> "${lxdTestFile}"
  s3cmdrun "${s3Endpoint}" "${lxd_backend}" "${initAccessKey}" "${initSecretKey}" put "${lxdTestFile}" "s3://${bucketPrefix}.foo"

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

  # Note original bucket size.
  orig_size="$(lxc storage bucket get "${poolName}" "${bucketPrefix}.foo" size)"
  orig_md5sum="$(s3cmdrun "${s3Endpoint}" "${lxd_backend}" "${initAccessKey}" "${initSecretKey}" info "s3://${bucketPrefix}.foo/${lxdTestFile}" | awk '{ if ($1 == "MD5") {print $3}}')  -"


  # Delete bucket and import exported bucket.
  lxc storage bucket delete "${poolName}" "${bucketPrefix}.foo"
  lxc storage bucket import "${poolName}" "${LXD_DIR}/testbucket.tar.gz" "${bucketPrefix}.bar"
  rm "${LXD_DIR}/testbucket.tar.gz"

  # Note imported bucket size and md5sum
  imported_size="$(lxc storage bucket get "${poolName}" "${bucketPrefix}.bar" size)"
  imported_md5sum="$(s3cmdrun "${s3Endpoint}" "${lxd_backend}" "${initAccessKey}" "${initSecretKey}" info "s3://${bucketPrefix}.bar/${lxdTestFile}" | awk '{ if ($1 == "MD5") {print $3}}')  -"

  # Ensure original size and md5sum equivalent to imported.
  [ "${orig_size}" = "${imported_size}" ]
  [ "${orig_md5sum}" = "${imported_md5sum}" ]

  # Test listing bucket files via S3.
  s3cmdrun "${s3Endpoint}" "${lxd_backend}" "${initAccessKey}" "${initSecretKey}" ls "s3://${bucketPrefix}.bar" | grep -F "${lxdTestFile}"

  # Test getting admin key from bucket.
  lxc storage bucket key list "${poolName}" "${bucketPrefix}.bar" | grep -F "admin"

  # Clean up.
  lxc storage bucket delete "${poolName}" "${bucketPrefix}.bar"
  ! lxc storage bucket list "${poolName}" | grep -F "${bucketPrefix}.bar" || false
  ! lxc storage bucket show "${poolName}" "${bucketPrefix}.bar" || false

  delete_object_storage_pool "${poolName}"

  rm -rf "${LXD_DIR}/storage-bucket-export/"*
  rmdir "${LXD_DIR}/storage-bucket-export"
}
