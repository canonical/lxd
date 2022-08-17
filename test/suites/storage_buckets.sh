s3cmdrun () {
  accessKey="${1}"
  secreyKey="${2}"
  shift 2

  s3cmd \
    --access_key="${accessKey}" \
    --secret_key="${secreyKey}" \
    --host="${LXD_CEPH_CEPHOBJECT_RADOSGW}" \
    --host-bucket="${LXD_CEPH_CEPHOBJECT_RADOSGW}" \
    --no-ssl \
    "$@"
}

test_storage_buckets() {
  # shellcheck disable=2039,3043
  local lxd_backend

  lxd_backend=$(storage_backend "$LXD_DIR")

  # Currently only ceph radosgw buckets are supported.
  if [ "$lxd_backend" != "ceph" ]; then
    return
  fi

  # Only run cephobject bucket tests if LXD_CEPH_CEPHOBJECT_RADOSGW specified.
  if [ "$lxd_backend" = "ceph" ] && [ -z "${LXD_CEPH_CEPHOBJECT_RADOSGW:-}" ]; then
    echo "==> SKIP: storage bucket tests for cephobject as LXD_CEPH_CEPHOBJECT_RADOSGW not specified"
    return
  fi

  # Check cephobject.radosgsw.endpoint is required for cephobject pools.
  if [ "$lxd_backend" = "ceph" ]; then
    ! lxc storage create s3 cephobject || false
    lxc storage create s3 cephobject cephobject.radosgsw.endpoint="${LXD_CEPH_CEPHOBJECT_RADOSGW}"
  fi

  lxc storage show s3

  bucketPrefix="lxd$$"

  # Check bucket name validation.
  ! lxc storage bucket create s3 .foo || false
  ! lxc storage bucket create s3 fo || false
  ! lxc storage bucket create s3 fooooooooooooooooooooooooooooooooooooooooooooooooooooooooooooooo || false
  ! lxc storage bucket create s3 "foo bar" || false

  # Create bucket.
  lxc storage bucket create s3 "${bucketPrefix}.foo" user.foo=comment
  lxc storage bucket list s3 | grep -F "${bucketPrefix}.foo"
  lxc storage bucket show s3 "${bucketPrefix}.foo"

  # Create bucket keys.

  # Create admin key with randomly generated credentials.
  creds=$(lxc storage bucket key create s3 "${bucketPrefix}.foo" admin-key --role=admin)
  adAccessKey=$(echo "${creds}" | awk '{ if ($1 == "Access" && $2 == "key:") {print $3}}')
  adSecretKey=$(echo "${creds}" | awk '{ if ($1 == "Secret" && $2 == "key:") {print $3}}')

  # Create read-only key with manually specified credentials.
  creds=$(lxc storage bucket key create s3 "${bucketPrefix}.foo" ro-key --role=read-only --access-key="${bucketPrefix}.foo.ro" --secret-key="password")
  roAccessKey=$(echo "${creds}" | awk '{ if ($1 == "Access" && $2 == "key:") {print $3}}')
  roSecretKey=$(echo "${creds}" | awk '{ if ($1 == "Secret" && $2 == "key:") {print $3}}')

  lxc storage bucket key list s3 "${bucketPrefix}.foo" | grep -F "admin-key"
  lxc storage bucket key list s3 "${bucketPrefix}.foo" | grep -F "ro-key"
  lxc storage bucket key show s3 "${bucketPrefix}.foo" admin-key
  lxc storage bucket key show s3 "${bucketPrefix}.foo" ro-key

  # Test listing buckets via S3.
  s3cmdrun "${adAccessKey}" "${adSecretKey}" ls | grep -F "${bucketPrefix}.foo"
  s3cmdrun "${roAccessKey}" "${roSecretKey}" ls | grep -F "${bucketPrefix}.foo"

  # Test making buckets via S3 is blocked.
  ! s3cmdrun "${adAccessKey}" "${adSecretKey}" mb "s3://${bucketPrefix}.foo2" || false
  ! s3cmdrun "${roAccessKey}" "${roSecretKey}" mb "s3://${bucketPrefix}.foo2" || false

  # Test putting a file into a bucket.
  echo "hello world ${bucketPrefix}" > "${bucketPrefix}.txt"
  s3cmdrun "${adAccessKey}" "${adSecretKey}" put "${bucketPrefix}.txt" "s3://${bucketPrefix}.foo"
  ! s3cmdrun "${roAccessKey}" "${roSecretKey}" put "${bucketPrefix}.txt" "s3://${bucketPrefix}.foo" || false

  # Test listing bucket files via S3.
  s3cmdrun "${adAccessKey}" "${adSecretKey}" ls "s3://${bucketPrefix}.foo" | grep -F "${bucketPrefix}.txt"
  s3cmdrun "${roAccessKey}" "${roSecretKey}" ls "s3://${bucketPrefix}.foo" | grep -F "${bucketPrefix}.txt"

  # Test getting a file from a bucket.
  s3cmdrun "${adAccessKey}" "${adSecretKey}" get "s3://${bucketPrefix}.foo/${bucketPrefix}.txt" "${bucketPrefix}.txt.get"
  rm "${bucketPrefix}.txt.get"
  s3cmdrun "${roAccessKey}" "${roSecretKey}" get "s3://${bucketPrefix}.foo/${bucketPrefix}.txt" "${bucketPrefix}.txt.get"
  rm "${bucketPrefix}.txt.get"

  # Test setting bucket policy to allow anonymous access.
  curl -sI "${LXD_CEPH_CEPHOBJECT_RADOSGW}/${bucketPrefix}.foo/${bucketPrefix}.txt" | grep -F "403 Forbidden"
  ! s3cmdrun "${roAccessKey}" "${roSecretKey}" setpolicy deps/s3_global_read_policy.json "s3://${bucketPrefix}.foo" || false
  s3cmdrun "${adAccessKey}" "${adSecretKey}" setpolicy deps/s3_global_read_policy.json "s3://${bucketPrefix}.foo"
  curl -sI "${LXD_CEPH_CEPHOBJECT_RADOSGW}/${bucketPrefix}.foo/${bucketPrefix}.txt" | grep -F "200 OK"
  ! s3cmdrun "${roAccessKey}" "${roSecretKey}" delpolicy "s3://${bucketPrefix}.foo" || false
  s3cmdrun "${adAccessKey}" "${adSecretKey}" delpolicy "s3://${bucketPrefix}.foo"
  curl -sI "${LXD_CEPH_CEPHOBJECT_RADOSGW}/${bucketPrefix}.foo/${bucketPrefix}.txt" | grep -F "403 Forbidden"

  # Test deleting a file from a bucket.
  ! s3cmdrun "${roAccessKey}" "${roSecretKey}" del "s3://${bucketPrefix}.foo/${bucketPrefix}.txt" || false
  s3cmdrun "${adAccessKey}" "${adSecretKey}" del "s3://${bucketPrefix}.foo/${bucketPrefix}.txt"

  # Test bucket quota.
  lxc storage bucket set s3 "${bucketPrefix}.foo" size=1
  ! s3cmdrun "${adAccessKey}" "${adSecretKey}" put "${bucketPrefix}.txt" "s3://${bucketPrefix}.foo" || false
  lxc storage bucket set s3 "${bucketPrefix}.foo" size=1MiB
  s3cmdrun "${adAccessKey}" "${adSecretKey}" put "${bucketPrefix}.txt" "s3://${bucketPrefix}.foo"
  s3cmdrun "${adAccessKey}" "${adSecretKey}" del "s3://${bucketPrefix}.foo/${bucketPrefix}.txt"
  rm "${bucketPrefix}.txt"

  # Test deleting bucket via s3.
  ! s3cmdrun "${roAccessKey}" "${roSecretKey}" rb "s3://${bucketPrefix}.foo" || false
  s3cmdrun "${adAccessKey}" "${adSecretKey}" rb "s3://${bucketPrefix}.foo"

  # Delete bucket keys.
  lxc storage bucket key delete s3 "${bucketPrefix}.foo" admin-key
  lxc storage bucket key delete s3 "${bucketPrefix}.foo" ro-key
  ! lxc storage bucket key list s3 "${bucketPrefix}.foo" | grep -F "admin-key" || false
  ! lxc storage bucket key list s3 "${bucketPrefix}.foo" | grep -F "ro-key" || false
  ! lxc storage bucket key show s3 "${bucketPrefix}.foo" admin-key || false
  ! lxc storage bucket key show s3 "${bucketPrefix}.foo" ro-key || false
  ! s3cmdrun "${adAccessKey}" "${adSecretKey}" ls || false
  ! s3cmdrun "${roAccessKey}" "${roSecretKey}" ls || false

  # Delete bucket.
  lxc storage bucket delete s3 "${bucketPrefix}.foo"
  ! lxc storage bucket list s3 | grep -F "${bucketPrefix}.foo" || false
  ! lxc storage bucket show s3 "${bucketPrefix}.foo" || false

  lxc storage delete s3
}
