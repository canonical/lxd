test_image_expiry() {
  # shellcheck disable=2039,3043
  local LXD2_DIR LXD2_ADDR
  LXD2_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD2_DIR}"
  spawn_lxd "${LXD2_DIR}" true
  LXD2_ADDR=$(cat "${LXD2_DIR}/lxd.addr")

  ensure_import_testimage

  # shellcheck disable=2153
  lxc_remote remote add l1 "${LXD_ADDR}" --accept-certificate --password foo
  lxc_remote remote add l2 "${LXD2_ADDR}" --accept-certificate --password foo

  # Create containers from a remote image in two projects.
  lxc_remote project create l2:p1 -c features.images=true -c features.profiles=false
  lxc_remote init l1:testimage l2:c1 --project default
  lxc_remote project switch l2:p1
  lxc_remote init l1:testimage l2:c2
  lxc_remote project switch l2:default

  fp="$(lxc_remote image info testimage | awk '/^Fingerprint/ {print $2}')"

  # Confirm the image is cached
  [ -n "${fp}" ]
  fpbrief=$(echo "${fp}" | cut -c 1-12)
  lxc_remote image list l2: | grep -q "${fpbrief}"

  # Test modification of image expiry date
  lxc_remote image info "l2:${fp}" | grep -q "Expires.*never"
  lxc_remote image show "l2:${fp}" | sed "s/expires_at.*/expires_at: 3000-01-01T00:00:00-00:00/" | lxc_remote image edit "l2:${fp}"
  lxc_remote image info "l2:${fp}" | grep -q "Expires.*3000"

  # Override the upload date for the image record in the default project.
  LXD_DIR="$LXD2_DIR" lxd sql global "UPDATE images SET last_use_date='$(date --rfc-3339=seconds -u -d "2 days ago")' WHERE fingerprint='${fp}' AND project_id = 1" | grep -q "Rows affected: 1"

  # Trigger the expiry
  lxc_remote config set l2: images.remote_cache_expiry 1

  for _ in $(seq 20); do
    sleep 1
    ! lxc_remote image list l2: | grep -q "${fpbrief}" && break
  done

  ! lxc_remote image list l2: | grep -q "${fpbrief}" || false

  # Check image is still in p1 project and has not been expired.
  lxc_remote image list l2: --project p1 | grep -q "${fpbrief}"

  # Test instance can still be created in p1 project.
  lxc_remote project switch l2:p1
  lxc_remote init l1:testimage l2:c3
  lxc_remote project switch l2:default

  # Override the upload date for the image record in the p1 project.
  LXD_DIR="$LXD2_DIR" lxd sql global "UPDATE images SET last_use_date='$(date --rfc-3339=seconds -u -d "2 days ago")' WHERE fingerprint='${fp}' AND project_id > 1" | grep -q "Rows affected: 1"
  lxc_remote project set l2:p1 images.remote_cache_expiry=1

  # Trigger the expiry in p1 project by changing global images.remote_cache_expiry.
  lxc_remote config unset l2: images.remote_cache_expiry

  for _ in $(seq 20); do
    sleep 1
    ! lxc_remote image list l2: --project p1 | grep -q "${fpbrief}" && break
  done

  ! lxc_remote image list l2: --project p1 | grep -q "${fpbrief}" || false

  # Cleanup and reset
  lxc_remote delete -f l2:c1
  lxc_remote delete -f l2:c2 --project p1
  lxc_remote delete -f l2:c3 --project p1
  lxc_remote project delete l2:p1
  lxc_remote remote remove l1
  lxc_remote remote remove l2
  kill_lxd "$LXD2_DIR"
}

test_image_list_all_aliases() {
    ensure_import_testimage
    # shellcheck disable=2039,2034,2155,3043
    local sum="$(lxc image info testimage | awk '/^Fingerprint/ {print $2}')"
    lxc image alias create zzz "$sum"
    # both aliases are listed if the "aliases" column is included in output
    lxc image list -c L | grep -qwF testimage
    lxc image list -c L | grep -qwF zzz
}

test_image_list_remotes() {
    # list images from the `images:` and `ubuntu-minimal:`  builtin remotes if they are reachable

    lxc remote list -f csv | while IFS=, read -r name url _; do
        if [ "${name}" != "images" ] && [ "${name}" != "ubuntu-minimal" ]; then
            continue
        fi

        # Check if there is connectivity
        curl --head --silent "${url}" > /dev/null || continue

        lxc image list "${name}:" > /dev/null
    done
}

test_image_import_dir() {
    ensure_import_testimage
    lxc image export testimage
    # shellcheck disable=2039,2034,2155,3043
    local image="$(ls -1 -- *.tar.xz)"
    mkdir -p unpacked
    tar -C unpacked -xf "$image"
    # shellcheck disable=2039,2034,2155,3043
    local fingerprint="$(lxc image import unpacked | awk '{print $NF;}')"
    rm -rf "$image" unpacked

    lxc image export "$fingerprint"
    # shellcheck disable=2039,2034,2155,3043
    local exported="${fingerprint}.tar.xz"

    tar tvf "$exported" | grep -Fq metadata.yaml
    rm "$exported"
}

test_image_import_existing_alias() {
    ensure_import_testimage
    lxc init testimage c
    lxc publish c --alias newimage --alias image2
    lxc delete c
    lxc image export testimage testimage.file
    lxc image delete testimage
    # XXX: ensure_import_testimage imports a `.tar.xz` image which is why once exported, those extensions are appended
    # the image can be imported with an existing alias
    lxc image import testimage.file.tar.xz --alias newimage
    rm testimage.file.tar.xz
    lxc image delete newimage image2
}

test_image_import_metadata() {
  local tmpDir imgDir imgTar out

  sub_test "Reject metadata.yaml that is a symlink pointing outside the archive"

  tmpDir=$(mktemp -d -p "${TEST_DIR}" XXX)
  imgDir="${tmpDir}/image"
  imgTar="${tmpDir}/image.tar"

  mkdir -p "${imgDir}/rootfs"

  # Create metadata.yaml as a symlink pointing outside the archive.
  ln -s "/etc/hostname" "${imgDir}/metadata.yaml"

  tar -cf "${imgTar}" -C "${imgDir}" .

  out="$(! lxc image import "${imgTar}" --alias image-invalid-metadata 2>&1 || false)"
  echo "${out}" | grep -F 'Error: Cannot read non-regular file "./metadata.yaml"'

  # Check the rejected image was not imported.
  ! lxc image list -f csv -c l | grep -qF "image-invalid-metadata" || false

  rm -rf "${tmpDir}"
}

test_image_metadata_confined() {
  local ct_name err_msg
  local ct_meta_path

  ct_name="c1"

  # The full error the client receives from any confined os.Root operation on the escaping symlink.
  err_msg="Error: openat metadata.yaml: path escapes from parent"

  ensure_import_testimage

  # Plant an unconfined metadata.yaml file into the container's drive whilst it is mounted.
  lxc init testimage "${ct_name}"
  lxc start "${ct_name}"
  # shellcheck disable=SC2153
  ct_meta_path="$(realpath "${LXD_DIR}/containers/${ct_name}/metadata.yaml")"
  rm -f "${ct_meta_path}"
  ln -s /etc/hostname "${ct_meta_path}"
  lxc stop -f "${ct_name}"

  # instanceMetadataGet (os.Root.Open): GET /1.0/instances/<name>/metadata.
  sub_test "Reject reading metadata.yaml symlink escaping the instance root on show"
  out="$(! lxc config metadata show "${ct_name}" 2>&1 || false)"
  echo "${out}" | grep -F "${err_msg}"

  # instanceMetadataPatch (os.Root.Open): PATCH /1.0/instances/<name>/metadata.
  sub_test "Reject reading metadata.yaml symlink escaping the instance root on patch"
  out="$(! lxc query -X PATCH -d '{"properties": {"os": "test"}}' "/1.0/instances/${ct_name}/metadata" 2>&1 || false)"
  echo "${out}" | grep -F "${err_msg}"

  # doInstanceMetadataUpdate (os.Root.WriteFile): PUT /1.0/instances/<name>/metadata.
  sub_test "Reject writing metadata.yaml symlink escaping the instance root on update"
  out="$(! lxc query -X PUT -d '{}' "/1.0/instances/${ct_name}/metadata" 2>&1 || false)"
  echo "${out}" | grep -F "${err_msg}"

  # lxc.Export (os.Root.Open): publishing the instance as an image reads its metadata.
  sub_test "Reject reading metadata.yaml symlink escaping the container root on publish"
  out="$(! lxc publish "${ct_name}" 2>&1 || false)"
  echo "${out}" | grep -F "${err_msg}"

  # lxc.templateApplyNow (os.Root.Open): starting the instance triggers templating which should be rejected.
  sub_test "Reject starting the instance whose metadata.yaml symlink escapes the instance root"
  if lxc start "${ct_name}"; then
    echo "ERROR: start must have been rejected"
    exit 1
  fi

  lxc delete -f "${ct_name}"
  lxc image delete testimage
}

test_image_backup_confined() {
  local ct_name ct_backup_path err_msg pool_driver pool_name

  ct_name="c1"
  err_msg="openat backup.yaml: path escapes from parent"

  ensure_import_testimage

  # Plant an unconfined backup.yaml file into the container's drive whilst it is mounted.
  lxc init testimage "${ct_name}"
  lxc start "${ct_name}"
  # shellcheck disable=SC2153
  ct_backup_path="$(realpath "${LXD_DIR}/containers/${ct_name}/backup.yaml")"
  mv "${ct_backup_path}" "${ct_backup_path}.backup"
  ln -s /etc/hostname "${ct_backup_path}"
  lxc stop -f "${ct_name}"

  # UpdateInstanceBackupFile (os.Root.WriteFile): starting the instance rewrites backup.yaml just
  # before the instance process starts, so a symlinked backup.yaml must not be followed outside
  # the instance's storage volume.
  sub_test "Reject writing backup.yaml symlink escaping the instance root on start"
  [[ "$(lxc start "${ct_name}" 2>&1 || false)" == *"${err_msg}"* ]]

  # UpdateInstanceBackupFile (os.Root.WriteFile): creating a snapshot also rewrites backup.yaml.
  sub_test "Reject writing backup.yaml symlink escaping the instance root on snapshot create"
  [[ "$(lxc snapshot "${ct_name}" snap0 2>&1 || false)" == *"${err_msg}"* ]]

  # Only test with the dir driver as we can easily cleanup after corrupting the container.
  pool_name="$(lxc profile device get default root pool)"
  pool_driver="$(lxc storage show "${pool_name}" | awk '/^driver:/ {print $2}')"
  if [ "${pool_driver}" = "dir" ]; then
    # Remove the instance and its storage volume DB records so recovery treats the volume as
    # unknown and attempts to parse its (symlinked) backup.yaml.
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM instances WHERE name='c1'"
    lxd sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='c1'"

    # detectUnknownInstanceVolume/ParseConfigYamlFile (os.Root.ReadFile): recovery reads
    # backup.yaml from within the volume's mount path. A symlink escaping the volume must be
    # rejected rather than followed, and recovery of this instance must fail rather than
    # silently leaking the symlink target's contents into a recovered instance record.
    if out=$(cat <<EOF | lxd recover 2>&1
no
yes
yes
EOF
    ); then
      echo "ERROR: lxd recover unexpectedly succeeded despite backup.yaml escaping the volume" >&2
      exit 1
    fi

    [[ "${out}" == *"${err_msg}"* ]]

    # At this stage the container is broken.
    # Fix the backup.yaml manually.
    # shellcheck disable=SC2153
    rm -rf "${LXD_DIR}/storage-pools/${pool_name}/containers/${ct_name}/backup.yaml"
    mv "${ct_backup_path}.backup" "${ct_backup_path}"

    # Now recover the container.
    cat <<EOF | lxd recover
no
yes
yes
EOF
  fi

  lxc delete -f "${ct_name}"
  lxc image delete testimage
}

test_image_refresh() {
  # shellcheck disable=2039,3043
  local LXD2_DIR LXD2_ADDR
  LXD2_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${LXD2_DIR}"
  spawn_lxd "${LXD2_DIR}" true
  LXD2_ADDR=$(cat "${LXD2_DIR}/lxd.addr")

  ensure_import_testimage

  lxc_remote remote add l2 "${LXD2_ADDR}" --accept-certificate --password foo

  poolDriver="$(lxc storage show "$(lxc profile device get default root pool)" | awk '/^driver:/ {print $2}')"

  # Publish image
  lxc image copy testimage l2: --alias testimage --public
  fp="$(lxc image info l2:testimage | awk '/Fingerprint: / {print $2}')"
  lxc image rm testimage

  # Create container from published image
  lxc init l2:testimage c1

  # Create an alias for the received image
  lxc image alias create testimage "${fp}"

  # Change image and publish it
  lxc init l2:testimage l2:c1
  echo test | lxc file push - l2:c1/tmp/testfile
  lxc publish l2:c1 l2: --alias testimage --reuse --public
  new_fp="$(lxc image info l2:testimage | awk '/Fingerprint: / {print $2}')"

  # Ensure the images differ
  [ "${fp}" != "${new_fp}" ]

  # Check original image exists before refresh.
  lxc image info "${fp}"

  if [ "${poolDriver}" != "dir" ]; then
    # Check old storage volume record exists and new one doesn't.
    lxd sql global 'select name from storage_volumes' | grep "${fp}"
    ! lxd sql global 'select name from storage_volumes' | grep "${new_fp}" || false
  fi

  # Refresh image
  lxc image refresh testimage

  # Ensure the old image is gone.
  ! lxc image info "${fp}" || false

  if [ "${poolDriver}" != "dir" ]; then
    # Check old storage volume record has been replaced with new one.
    ! lxd sql global 'select name from storage_volumes' | grep "${fp}" || false
    lxd sql global 'select name from storage_volumes' | grep "${new_fp}"
  fi

  # Cleanup
  lxc rm l2:c1
  lxc rm c1
  lxc remote rm l2
  kill_lxd "${LXD2_DIR}"
}

test_image_with_rootfs_symlink() {
  local tmpDir imgDir imgTar format out

  for format in gnu ustar pax; do
    sub_test "Reject top-level symlink during rootfs unpack (${format})"

    imgDir=$(create_image_with_rootfs_symlink "${format}")
    imgTar="${imgDir}/image.tar"

    lxc image import "${imgTar}" --alias image-rootfs-symlink
    lxc init image-rootfs-symlink c-symlink

    # Once instance is created, create file on host FS.
    tmpDir=$(mktemp -d -p "${TEST_DIR}" XXX)
    echo "This is a file on the host FS" > "${imgDir}/hostfs.txt"

    # Try pulling the file from the instance and ensure the injected symlink is not followed.
    if out=$(lxc file pull c-symlink/hostfs.txt "${tmpDir}/hostfs.txt" 2>&1); then
      echo "ERROR: Pulling file with a top-level rootfs symlink unexpectedly succeeded" >&2
      exit 1
    fi

    echo "${out}" | grep -q "path escapes from parent"

    # Cleanup.
    lxc delete -f c-symlink
    lxc image delete image-rootfs-symlink

    rm -rf "${tmpDir}"
    rm -rf "${imgDir}"
  done
}

create_image_with_rootfs_symlink() {
  local tarFormat tmpDir imgDir imgTar
  tarFormat="${1:-gnu}"

  # Setup directory.
  tmpDir=$(mktemp -d -p "${TEST_DIR}" XXX)
  imgDir="${tmpDir}/image"
  imgTar="${tmpDir}/image.tar"

  # Build image tarball.
  mkdir -p "${imgDir}/rootfs"
  printf '%s\n' "architecture: $(uname -m)" "creation_date: 1" > "${imgDir}/metadata.yaml"
  tar \
    --format="${tarFormat}" \
    -cf "${imgTar}" \
    -C "${imgDir}" .

  # Append rootfs symlink to image tarball.
  (
    cd "${imgDir}" || exit
    rmdir "rootfs"
    ln -s "${tmpDir}" "rootfs"
    tar -f "${imgTar}" --append "./rootfs"
  )

  rm -rf "${imgDir}"
  echo "${tmpDir}"
}

test_image_with_templates_symlink() {
  local tmpDir imgDir format tplListOut tplShowOut tplCreateOut tplDeleteOut expectErr
  expectErr="openat templates: path escapes from parent"

  for format in gnu ustar pax; do
    sub_test "Reject top-level symlink for templates directory (${format})"

    imgDir=$(create_image_with_templates_symlink "${format}")

    # Import image and initialize container.
    lxc image import "${imgDir}/image.tar" --alias image-templates-symlink
    lxc init image-templates-symlink c-symlink

    # Attempt creating and reading template file from the instance with injected top-level symlink.
    if tplListOut=$(lxc config template list c-symlink 2>&1); then
      echo "ERROR: Listing templates with a top-level symlink unexpectedly succeeded" >&2
      exit 1
    fi

    if tplShowOut=$(lxc config template show c-symlink non-existing 2>&1); then
      echo "ERROR: Showing a template with a top-level symlink unexpectedly succeeded" >&2
      exit 1
    fi

    if tplCreateOut=$(lxc config template create c-symlink tpl1 2>&1); then
      echo "ERROR: Creating a template with a top-level symlink unexpectedly succeeded" >&2
      exit 1
    fi

    if tplDeleteOut=$(lxc config template delete c-symlink tpl1 2>&1); then
      echo "ERROR: Deleting a template with a top-level symlink unexpectedly succeeded" >&2
      exit 1
    fi

    echo "${tplListOut}" | grep "${expectErr}"
    echo "${tplShowOut}" | grep "${expectErr}"
    echo "${tplDeleteOut}" | grep "${expectErr}"
    echo "${tplCreateOut}" | grep "${expectErr}"

    lxc delete -f c-symlink
    lxc image delete image-templates-symlink

    rm -rf "${imgDir}"
  done
}

create_image_with_templates_symlink() {
  local tarFormat tmpDir imgDir imgTar
  tarFormat="${1:-gnu}"

  # Setup directory.
  tmpDir=$(mktemp -d -p "${TEST_DIR}" XXX)
  imgDir="${tmpDir}/image"
  imgTar="${tmpDir}/image.tar"

  mkdir -p "${imgDir}/rootfs"

  # Content.
  ln -s "${tmpDir}" "${imgDir}/templates"
  echo "This is rootfs" > "${imgDir}/rootfs/rootfs.txt"
  printf '%s\n' "architecture: $(uname -m)" "creation_date: 1" > "${imgDir}/metadata.yaml"

  # Build combined image tarball (rootfs + metadata).
  tar \
    --format="${tarFormat}" \
    -cf "${imgTar}" \
    -C "${imgDir}" .

  rm -rf "${imgDir}"
  echo "${tmpDir}"
}

test_image_import_metadata_not_regular_file() {
  local tmpDir imgDir imgTar out

  sub_test "Reject metadata that is overridden with a non-regular file when unpacking into an instance volume"

  for m in metadata.yaml backup.yaml; do
    tmpDir=$(mktemp -d -p "${TEST_DIR}" XXX)
    imgDir="${tmpDir}/image"
    imgTar="${tmpDir}/image.tar"

    # Build an image tarball with a valid metadata.yaml.
    # The metadata.yaml always has to be present.
    mkdir -p "${imgDir}/rootfs"
    printf '%s\n' "architecture: $(uname -m)" "creation_date: 1" > "${imgDir}/metadata.yaml"
    tar -cf "${imgTar}" -C "${imgDir}" .

    # Append the non-regular metadata file to the archive.
    # In case of the metadata.yaml, this will allow importing the image as the first regular metadata.yaml passes the checks.
    # But the second metadata.yaml will persist on the filesystem after unpacking the image which will trigger the error below.
    if [ ! "${m}" = "backup.yaml" ]; then
        # As the metadata.yaml is always present, remove it before creating the symlink with the same name.
        rm "${imgDir}/${m}"
    fi
    ln -s "/etc/hostname" "${imgDir}/${m}"
    tar -f "${imgTar}" --append -C "${imgDir}" "./${m}"

    lxc image import "${imgTar}" --alias image-invalid-metadata

    # Unpacking the image into the instance's storage volume must reject the non-regular metadata file.
    if out=$(lxc init image-invalid-metadata c1 2>&1); then
        echo "ERROR: Initializing an instance from an image with a non-regular metadata file unexpectedly succeeded" >&2
        exit 1
    fi

    echo "${out}" | grep -qF "is not a regular file"

    lxc image delete image-invalid-metadata
    rm -rf "${tmpDir}"
  done
}
