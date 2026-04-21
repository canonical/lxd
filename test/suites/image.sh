test_image_expiry() {
  local LXD2_DIR LXD2_ADDR
  # shellcheck disable=2153
  LXD2_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  spawn_lxd "${LXD2_DIR}" true
  LXD2_ADDR=$(< "${LXD2_DIR}/lxd.addr")

  ensure_import_testimage

  # Mark the image as public.
  lxc image show testimage | sed -e "s/^public: false/public: true/" | lxc image edit testimage

  local token
  token="$(LXD_DIR=${LXD2_DIR} lxc config trust add --name foo -q)"
  # shellcheck disable=2153
  lxc_remote remote add l1 "${LXD2_ADDR}" --token "${token}"

  # shellcheck disable=2153
  # Create an image registry on the second LXD backed by a public cluster link to this LXD.
  pending="$(LXD_DIR=${LXD2_DIR} lxc query --request POST /1.0/cluster/links --data "{\"name\":\"img-link\",\"type\":\"public\",\"remote_address\":\"${LXD_ADDR}\"}")"
  link_cert="$(echo "${pending}" | jq --exit-status '.certificate')"
  LXD_DIR=${LXD2_DIR} lxc query --request POST /1.0/cluster/links --data "{\"name\":\"img-link\",\"type\":\"public\",\"remote_address\":\"${LXD_ADDR}\",\"cluster_certificate\":${link_cert}}" > /dev/null
  lxc_remote image registry create l1:img cluster=img-link source_project=default

  echo "Create containers from a remote image in three projects."
  lxc_remote project create l1:p1 -c features.images=true -c features.profiles=false
  lxc_remote init img:testimage l1:c1 --project default
  lxc_remote project switch l1:p1
  lxc_remote init img:testimage l1:c2
  lxc_remote project create l1:p2 -c features.images=true -c features.profiles=false
  lxc_remote project switch l1:p2

  local fp
  fp="$(lxc_remote image info testimage | awk '/^Fingerprint/ {print $2}')"

  echo "Create instance from cached image."
  lxc_remote init img:testimage l1:c3
  lxc_remote delete -f l1:c3

  echo "Confirm the image is cached."
  [ -n "${fp}" ]
  local fpbrief
  fpbrief=$(echo "${fp}" | cut -c 1-12)
  lxc_remote image list -f csv -c f l1: | grep -xF "${fpbrief}"

  echo "Project can still be deleted since cached images are pruned."
  lxc_remote project delete l1:p2

  echo "Switch back to default project and confirm image is still cached."
  lxc_remote project switch l1:default
  lxc_remote image list -f csv -c f l1: | grep -xF "${fpbrief}"

  echo "Test modification of image expiry date."
  lxc_remote image info "l1:${fp}" | grep "Expires.*never"
  lxc_remote image show "l1:${fp}" | sed "s/expires_at.*/expires_at: 3000-01-01T00:00:00-00:00/" | lxc_remote image edit "l1:${fp}"
  lxc_remote image info "l1:${fp}" | grep "Expires.*3000"

  echo "Override the upload date for the image record in the default project."
  LXD_DIR="$LXD2_DIR" lxd sql global "UPDATE images SET last_use_date='$(date --rfc-3339=seconds -u -d "2 days ago")' WHERE fingerprint='${fp}' AND project_id = 1" | grep -xF "Rows affected: 1"

  echo "Trigger the expiry."
  lxc_remote config set l1: images.remote_cache_expiry 1

  for _ in $(seq 40); do
    if ! lxc_remote image info l1:"${fpbrief}" 2>/dev/null; then
      break
    fi
    sleep 0.5
  done

  ! lxc_remote image info l1:"${fpbrief}" || false

  echo "Check image is still in p1 project and has not been expired."
  lxc_remote image list -f csv -c f l1: --project p1 | grep -xF "${fpbrief}"

  echo "Test instance can still be created in p1 project."
  lxc_remote project switch l1:p1
  lxc_remote init img:testimage l1:c3
  lxc_remote project switch l1:default

  echo "Override the upload date for the image record in the p1 project."
  LXD_DIR="$LXD2_DIR" lxd sql global "UPDATE images SET last_use_date='$(date --rfc-3339=seconds -u -d "2 days ago")' WHERE fingerprint='${fp}' AND project_id > 1" | grep -xF "Rows affected: 1"
  lxc_remote project set l1:p1 images.remote_cache_expiry=1

  echo "Trigger the expiry in p1 project by changing global images.remote_cache_expiry."
  lxc_remote config unset l1: images.remote_cache_expiry

  for _ in $(seq 40); do
    if ! lxc_remote image info l1:"${fpbrief}" --project p1 2>/dev/null; then
      break
    fi
    sleep 0.5
  done

  ! lxc_remote image info l1:"${fpbrief}" --project p1 || false

  echo "==> Clean up instances."
  lxc_remote delete -f l1:c1
  lxc_remote delete -f l1:c2 --project p1
  lxc_remote delete -f l1:c3 --project p1

  echo "==> Copy remote image to a local store in project p1. It should be marked non-cached."
  lxc_remote image copy img:testimage l1: --target-project p1

  echo "==> Check that the locally saved image in project p1 is marked as non-cached."
  lxc_remote image info l1:"${fp}" --project p1 | grep -xF "Cached: no"

  echo "==> Create a new instance in the default project with a remote image. It should be cached."
  lxc_remote init img:testimage l1:c1

  echo "==> Check that the locally saved image in default project is marked as cached."
  lxc_remote image info l1:"${fp}" | grep -xF "Cached: yes"

  echo "==> Check that the image file exists locally."
  ls -l "${LXD2_DIR}/images/${fp}"

  echo "==> Update last_use_date for both images to Jan 1, 2000."
  LXD_DIR="$LXD2_DIR" lxd sql global "UPDATE images SET last_use_date = '2000-01-01T00:00:00Z'" | grep -xF "Rows affected: 2"

  echo "==> Trigger the expiry. Image in the default project should get pruned, but image in project p1 should remain."
  lxc_remote config set l1: images.remote_cache_expiry 1

  for _ in $(seq 40); do
    if ! lxc_remote image info l1:"${fpbrief}" 2>/dev/null; then
      break
    fi
    sleep 0.5
  done

  echo "==> Check that image in the default project was deleted."
  ! lxc_remote image info l1:"${fpbrief}" || false

  echo "==> Check that image in project p1 still exists."
  lxc_remote image info l1:"${fpbrief}" --project p1

  echo "==> Check that the image file still exists locally because it is used by image in project p1."
  ls -l "${LXD2_DIR}/images/${fp}"

  echo "==> Create an instance in project p1 using locally saved image."
  lxc_remote init l1:"${fpbrief}" l1:c2 --project p1

  echo "==> Cleanup and reset."
  lxc_remote delete -f l1:c1
  lxc_remote delete -f l1:c2 --project p1
  lxc_remote image delete l1:"${fpbrief}" --project p1
  lxc_remote project delete l1:p1
  lxc_remote image registry delete l1:img
  lxc_remote remote remove l1
  kill_lxd "$LXD2_DIR"
}

test_image_list_all_aliases() {
    ensure_import_testimage
    local sum
    sum="$(lxc image info testimage | awk '/^Fingerprint/ {print $2}')"
    lxc image alias create zzz "$sum"
    # both aliases are listed if the "aliases" column is included in output
    local expected_output
    expected_output="$(echo -e '"testimage\nzzz"')"
    [ "$(lxc image list -f csv -c L)" = "${expected_output}" ]
    lxc image alias delete zzz
}

test_image_list_all_projects() {
    ensure_import_testimage

    local fingerprint
    fingerprint="$(lxc image info testimage | awk '/^Fingerprint/ {print $2}')"
    local fp_short
    fp_short="$(echo "${fingerprint}" | cut -c1-12)"

    sub_test "Same image in two projects appears twice in --all-projects output"

    # Create a second project and copy the same image into it.
    lxc project create p1 -c features.images=true -c features.profiles=false
    lxc image copy "${fingerprint}" local: --target-project p1

    # Verify both entries appear in the table.
    local count
    count="$(lxc image list --all-projects -f csv -c f | grep -cxF "${fp_short}")"
    [ "${count}" -eq 2 ]

    # Verify the project names are correct.
    lxc image list --all-projects -f csv -c ef | grep -xF "default,${fp_short}"
    lxc image list --all-projects -f csv -c ef | grep -xF "p1,${fp_short}"

    # Clean up.
    lxc image delete "${fingerprint}" --project p1
    lxc project delete p1
}

test_image_list_remotes() {
    # list images from the `images:` and `ubuntu-minimal:` builtin remotes if they are reachable
    lxc remote list -f csv | while IFS=, read -r name url _; do
        if [ "${name}" != "images" ] && [ "${name}" != "ubuntu-minimal" ]; then
            continue
        fi

        # Check if there is connectivity
        curl --head --silent "${url}" > /dev/null || continue

        lxc image list "${name}:" > /dev/null
    done
}

test_image_architectures() {
  local url="https://cloud-images.ubuntu.com/daily/"
  if ! curl --head --silent "${url}" > /dev/null; then
    export TEST_UNMET_REQUIREMENT="No connectivity to ${url}"
    return
  fi

  local arch suffix
  for arch in "" amd64 amd64v3 arm64 armhf ppc64el riscv64 s390x; do
    suffix="/${arch}"
    if [ "${arch}" = "" ]; then
      suffix=""
    fi

    lxc image show "ubuntu-daily:26.04${suffix}"
  done
}

test_image_import_dir() {
    mkdir -p unpacked
    tar -C unpacked -xf "${LXD_TEST_IMAGE}"
    local fingerprint
    fingerprint="$(lxc image import unpacked | awk '{print $NF;}')"
    rm -rf unpacked

    lxc image export "${fingerprint}"
    lxc image delete "${fingerprint}"

    tar tvf "${fingerprint}.tar"* --occurrence=1 metadata.yaml
    rm "${fingerprint}.tar"*
}

test_image_import_url() {
    sub_test "Verify image import from URL is rejected by the client"

    output="$(! lxc image import https://example.com/image.tar.gz 2>&1 || false)"
    echo "${output}" | grep -F "Image download from client-specified URL is not supported"

    sub_test "Verify image import from URL is rejected by the server"

    # Sending a request with source type "url" directly to the server should return a 400 error.
    # shellcheck disable=2153
    curl --silent --unix-socket "${LXD_DIR}/unix.socket" -X POST "lxd/1.0/images" \
        -H "Content-Type: application/json" \
        --data '{"source": {"type": "url", "url": "https://example.com/image.tar.gz"}}' \
        | jq --exit-status '.error_code == 400 and .error == "Image download from client-specified URL is not supported"'
}

test_image_import_existing_alias() {
    ensure_import_testimage
    lxc init testimage c
    lxc publish c --alias newimage --alias image2
    lxc delete c
    lxc image export testimage testimage.file
    lxc image delete testimage

    # XXX: ensure_import_testimage imports a `.tar` image which is why once exported, those extensions are appended
    # the image can be imported with an existing alias
    lxc image import testimage.file.tar* --alias newimage

    # Test for proper error message when importing an image to a non-existing project
    output="$(! lxc image import testimage.file.tar* --project nonexistingproject 2>&1 || false)"
    echo "${output}" | grep -F "Project not found"

    rm testimage.file.tar*
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

  out="$(! lxc image import "${imgTar}" 2>&1 || false)"
  echo "${out}" | grep -F 'Error: Cannot read non-regular file "./metadata.yaml"'

  # Check the list of images is empty.
  [ "$(lxc image list -f csv -c f | wc -l)" -eq 0 ]

  rm -rf "${tmpDir}"
}

test_image_metadata_confined() {
  local ct_name err_msg start_err_msg vm_name
  local ct_meta_path vm_meta_path

  ct_name="c1"

  # The full error the client receives from any confined os.Root operation on the escaping symlink.
  err_msg="Error: openat metadata.yaml: path escapes from parent"
  start_err_msg="Error: Failed applying template: openat metadata.yaml: path escapes from parent"

  ensure_import_testimage

  # Plant an unconfined metadata.yaml file into the container's drive whilst it is mounted.
  lxc init testimage "${ct_name}"
  lxc start "${ct_name}"
  # shellcheck disable=2153
  ct_meta_path="$(realpath "${LXD_DIR}/containers/${ct_name}/metadata.yaml")"
  rm -f "${ct_meta_path}"
  ln -s /etc/hostname "${ct_meta_path}"
  lxc stop -f "${ct_name}"

  # instanceMetadataGet (os.Root.Open): GET /1.0/instances/<name>/metadata.
  sub_test "Reject reading metadata.yaml symlink escaping the instance root on show"
  [ "$(! "${_LXC}" config metadata show "${ct_name}" 2>&1 1>/dev/null || false)" = "${err_msg}" ]

  # instanceMetadataPatch (os.Root.Open): PATCH /1.0/instances/<name>/metadata.
  sub_test "Reject reading metadata.yaml symlink escaping the instance root on patch"
  [ "$(! "${_LXC}" query -X PATCH -d '{"properties": {"os": "test"}}' "/1.0/instances/${ct_name}/metadata" 2>&1 1>/dev/null || false)" = "${err_msg}" ]

  # doInstanceMetadataUpdate (os.Root.WriteFile): PUT /1.0/instances/<name>/metadata.
  sub_test "Reject writing metadata.yaml symlink escaping the instance root on update"
  [ "$(! "${_LXC}" query -X PUT -d '{"architecture": "'"$(uname -m)"'", "creation_date": 1}' "/1.0/instances/${ct_name}/metadata" 2>&1 1>/dev/null || false)" = "${err_msg}" ]

  # lxc.Export (os.Root.Open): publishing the instance as an image reads its metadata.
  sub_test "Reject reading metadata.yaml symlink escaping the container root on publish"
  [ "$(! "${_LXC}" publish "${ct_name}" 2>&1 1>/dev/null || false)" = "${err_msg}" ]

  # lxc.templateApplyNow (os.Root.Open): starting the instance triggers templating which should be rejected.
  sub_test "Reject starting the instance whose metadata.yaml symlink escapes the instance root"
  if lxc start "${ct_name}"; then
    echo "ERROR: start must have been rejected"
    exit 1
  fi

  lxc delete -f "${ct_name}"

  # Also check VMs.
  if [ "${LXD_VM_TESTS}" != "0" ]; then
    vm_name="v1"

    # Plant an unconfined metadata.yaml file into the VM's config drive whilst it is mounted.
    lxc init "${vm_name}" --vm --empty --config limits.memory=384MiB
    lxc start "${vm_name}"
    vm_meta_path="$(realpath "${LXD_DIR}/virtual-machines/${vm_name}/metadata.yaml")"
    rm -f "${vm_meta_path}"
    ln -s /etc/hostname "${vm_meta_path}"
    lxc stop -f "${vm_name}"

    # qemu.Export (os.Root.Open): publishing the stopped VM reads its metadata.
    sub_test "Reject reading metadata.yaml symlink escaping the VM root on publish"
    [ "$(! "${_LXC}" publish "${vm_name}" 2>&1 1>/dev/null || false)" = "${err_msg}" ]

    # qemu.templateApplyNow (os.Root.Open) is only reachable when the VM is started.
    sub_test "Reject starting the VM whose metadata.yaml symlink escapes the instance root"
    [ "$(! "${_LXC}" start "${vm_name}" 2>&1 1>/dev/null || false)" = "${start_err_msg}
Try \`lxc info --show-log ${vm_name}\` for more info" ]

    lxc delete -f "${vm_name}"
  fi

  lxc image delete testimage
}

test_image_metadata_template_target_confined() {
  local ct_name target_dir target_file target_content escaping_path

  ensure_import_testimage

  ct_name="c1"

  # A root-owned file living clearly outside of any instance root. If the
  # confinement is bypassed, template application would overwrite it.
  target_dir="$(mktemp -d -p "${TEST_DIR}" XXX)"
  target_file="${target_dir}/ROOT_OWNED_TARGET"
  target_content="legitimate root-owned system file"
  printf '%s\n' "${target_content}" > "${target_file}"
  chown root:root "${target_file}"
  chmod 0600 "${target_file}"

  # The template target path uses a bogus first component followed by enough
  # ".." segments to climb above the instance rootfs and land on the absolute
  # path of the root-owned file. The confined os.Root must reject this before
  # the escaping path can be opened.
  escaping_path="/nonexistent/../../../../../../../../../../../../..${target_file}"

  lxc init testimage "${ct_name}"

  sub_test "Upload a template file to the instance via the metadata templates API"
  printf '#!/bin/sh\n# OVERWRITTEN VIA LXD TEMPLATE ESCAPE\nexit 0\n' \
    | curl --silent --fail --unix-socket "${LXD_DIR}/unix.socket" -X POST \
      -H "Content-Type: application/octet-stream" --data-binary @- \
      "lxd/1.0/instances/${ct_name}/metadata/templates?path=escape.tpl" \
    | jq --exit-status '.status_code == 200'

  sub_test "Register a template whose target path escapes the instance root"
  "${_LXC}" query -X PUT -d "{\"architecture\": \"$(uname -m)\", \"creation_date\": 1, \"properties\": {}, \"templates\": {\"${escaping_path}\": {\"when\": [\"start\"], \"create_only\": false, \"template\": \"escape.tpl\", \"properties\": {}}}}" "/1.0/instances/${ct_name}/metadata"

  # templateApplyNow (os.Root.OpenFile): starting the instance applies "start"
  # templates. The escaping target must be rejected rather than followed out of
  # the instance root. The container start hook surfaces only a generic failure
  # to the client, so assert that start fails and check the instance start log
  # for the confinement error before confirming the root-owned file was left
  # untouched.
  sub_test "Reject starting the instance whose template target escapes the instance root"
  if lxc start "${ct_name}"; then
    echo "ERROR: start must have been rejected"
    exit 1
  fi

  sub_test "Confirm the daemon log reports the template confinement error"
  # The daemon shares one logrus formatter between stderr and the logfile, and it enables colors
  # whenever stderr is a terminal. That embeds ANSI escape codes around the field keys in lxd.log
  # (e.g. instance=/project=), so strip them before matching the confinement error.
  sed 's/\x1b\[[0-9;]*m//g' "${LXD_DIR}/lxd.log" | grep -F "ROOT_OWNED_TARGET: path escapes from parent\" instance=${ct_name} project=default" >/dev/null

  sub_test "Confirm the root-owned file outside the instance root was not modified"
  [ "$(cat "${target_file}")" = "${target_content}" ]

  lxc delete -f "${ct_name}"

  # Also check VMs. The QEMU driver renders each template to "<template>.out"
  # inside the config drive, so the attacker-influenced value is the template
  # source name rather than the map key. A root-owned target file ending in
  # ".out" is planted so that the escaping source name resolves onto it.
  # templateApplyNow (os.Root.OpenFile) is only reached when the VM is started,
  # so gate it with LXD_VM_TESTS.
  if [ "${LXD_VM_TESTS}" != "0" ]; then
    local vm_name vm_target_file vm_target_content vm_escaping_template
    vm_name="v1"
    vm_target_file="${target_dir}/VM_ROOT_OWNED_TARGET.out"
    vm_target_content="legitimate root-owned system file for VM"
    printf '%s\n' "${vm_target_content}" > "${vm_target_file}"
    chown root:root "${vm_target_file}"
    chmod 0600 "${vm_target_file}"

    # The QEMU driver appends ".out" to the template source name, so point the
    # escaping source name at the target file with the ".out" suffix stripped.
    vm_escaping_template="/nonexistent/../../../../../../../../../../../../..${target_dir}/VM_ROOT_OWNED_TARGET"

    lxc init "${vm_name}" --vm --empty --config limits.memory=384MiB

    sub_test "Register a VM template whose source name escapes the instance root"
    "${_LXC}" query -X PUT -d "{\"architecture\": \"$(uname -m)\", \"creation_date\": 1, \"properties\": {}, \"templates\": {\"escape.tpl\": {\"when\": [\"start\"], \"create_only\": false, \"template\": \"${vm_escaping_template}\", \"properties\": {}}}}" "/1.0/instances/${vm_name}/metadata"

    sub_test "Reject starting the VM whose template source escapes the instance root"
    if lxc start "${vm_name}"; then
      echo "ERROR: start must have been rejected"
      exit 1
    fi

    sub_test "Confirm the daemon log reports the VM template confinement error"
    sed 's/\x1b\[[0-9;]*m//g' "${LXD_DIR}/lxd.log" | grep -F "VM_ROOT_OWNED_TARGET.out: path escapes from parent" >/dev/null

    sub_test "Confirm the root-owned file outside the VM root was not modified"
    [ "$(cat "${vm_target_file}")" = "${vm_target_content}" ]

    lxc delete -f "${vm_name}"
  fi

  lxc image delete testimage
  rm -rf "${target_dir}"
}

test_image_backup_confined() {
  local ct_name ct_backup_path err_msg pool_driver pool_name

  ct_name="c1"
  err_msg="openat backup.yaml: path escapes from parent"

  ensure_import_testimage

  # Plant an unconfined backup.yaml file into the container's drive whilst it is mounted.
  lxc init testimage "${ct_name}"
  lxc start "${ct_name}"
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
    rm -rf "${LXD_DIR}/storage-pools/${pool_name}/containers/${ct_name}/backup.yaml"
    mv "${ct_backup_path}.backup" "${ct_backup_path}"

    # Now recover the container.
    cat <<EOF | lxd recover
yes
yes
EOF
  fi

  lxc delete -f "${ct_name}"
  lxc image delete testimage
}

test_image_refresh() {
  local LXD2_DIR LXD2_ADDR
  LXD2_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  spawn_lxd "${LXD2_DIR}" true
  LXD2_ADDR=$(< "${LXD2_DIR}/lxd.addr")

  token="$(LXD_DIR=${LXD2_DIR} lxc config trust add --name foo -q)"
  lxc_remote remote add l2 "${LXD2_ADDR}" --token "${token}"

  # Create an image registry backed by a public cluster link to the second LXD.
  pending="$(lxc_remote query --request POST /1.0/cluster/links --data "{\"name\":\"img-link\",\"type\":\"public\",\"remote_address\":\"${LXD2_ADDR}\"}")"
  link_cert="$(echo "${pending}" | jq --exit-status '.certificate')"
  lxc_remote query --request POST /1.0/cluster/links --data "{\"name\":\"img-link\",\"type\":\"public\",\"remote_address\":\"${LXD2_ADDR}\",\"cluster_certificate\":${link_cert}}" > /dev/null
  lxc_remote image registry create img cluster=img-link source_project=default

  poolDriver="$(storage_backend "${LXD2_DIR}")"

  # Publish image
  LXD_DIR=${LXD2_DIR} deps/import-busybox --alias testimage --public
  fp="$(lxc image info l2:testimage | awk '/Fingerprint: / {print $2}')"

  # Create container from published image
  lxc init img:testimage c1

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
    lxd sql global 'SELECT name FROM storage_volumes' | grep -F "${fp}"
    ! lxd sql global 'SELECT name FROM storage_volumes' | grep -F "${new_fp}" || false
  fi

  # Refresh image
  lxc image refresh testimage

  # Ensure the old image is gone.
  ! lxc image info "${fp}" || false

  if [ "${poolDriver}" != "dir" ]; then
    # Check old storage volume record has been replaced with new one.
    ! lxd sql global 'SELECT name FROM storage_volumes' | grep -F "${fp}" || false
    lxd sql global 'SELECT name FROM storage_volumes' | grep -F "${new_fp}"
  fi

  # Cleanup
  lxc rm l2:c1
  lxc rm c1
  lxc image registry rm img
  lxc cluster link delete img-link
  lxc remote rm l2
  kill_lxd "${LXD2_DIR}"
}

test_images_public() {
  echo "==> Check public image handling for untrusted callers"
  run_images_public

  echo "==> Check public image handling for restricted TLS clients with no permissions"
  token="$(lxc config trust add --name tmp --restricted --quiet)"

  # Generate certificates.
  tmpdir="$(mktemp -d -p "${TEST_DIR}" XXX)"
  LXD_CONF="${tmpdir}" gen_cert_and_key client

  # Gain trust.
  LXD_CONF="${tmpdir}" lxc remote add tmp "${token}"

  # Rename certificates, this tells "run_images_public" which assertions to run.
  mv "${tmpdir}/client.crt" "${tmpdir}/restricted.crt"
  mv "${tmpdir}/client.key" "${tmpdir}/restricted.key"

  # Run test.
  CERT_DIR="${tmpdir}" CERT_NAME="restricted" run_images_public

  # Clean up.
  lxc config trust remove "$(cert_fingerprint "${tmpdir}/restricted.crt")"
  rm -rf "${tmpdir}"

  echo "==> Check public image handling for fine-grained TLS clients with no groups"
  token="$(lxc auth identity create tls/tmp --quiet)"

  # Generate certificates.
  tmpdir="$(mktemp -d -p "${TEST_DIR}" XXX)"
  LXD_CONF="${tmpdir}" gen_cert_and_key client

  # Gain trust.
  LXD_CONF="${tmpdir}" lxc remote add tmp "${token}"

  # Rename certificates, this tells "run_images_public" which assertions to run.
  mv "${tmpdir}/client.crt" "${tmpdir}/fine-grained.crt"
  mv "${tmpdir}/client.key" "${tmpdir}/fine-grained.key"

  # Run test.
  CERT_DIR="${tmpdir}" CERT_NAME="fine-grained" run_images_public

  # Clean up.
  lxc auth identity delete tls/tmp
  rm -rf "${tmpdir}"
}

run_images_public() {
  query() {
    # Function to use for querying. (Cant use my_curl for untrusted requests).
    # SC2068 is disabled because we do want arguments to be re-split.
    url="${1}"
    shift
    if [ -n "${CERT_DIR:-}" ]; then
      # shellcheck disable=2153,2068
      curl -s --cacert "${LXD_DIR}/server.crt" --cert "${CERT_DIR}/${CERT_NAME}.crt" --key "${CERT_DIR}/${CERT_NAME}.key" "https://${LXD_ADDR}${url}" ${@}
    else
      # shellcheck disable=2153,2068
      curl -s --cacert "${LXD_DIR}/server.crt" "https://${LXD_ADDR}${url}" ${@}
    fi
  }

  # Add a private image to the default project.
  deps/import-busybox --alias default-img

  # Create a project and import an image.
  lxc project create foo
  deps/import-busybox --project foo --alias foo-img

  # Marking a newly imported image as public is forbidden when the image is in a non-default project.
  if err_msg="$(deps/import-busybox --project foo --alias foo-public --public --template create 2>&1)"; then
    echo "ERROR: Importing a public image into a non-default project unexpectedly succeeded" >&2
    exit 1
  fi

  echo "${err_msg}" | grep -Fq "Images can only be marked public in the default project"

  # All callers see an empty list of images in the default project.
  query /1.0/images | jq --exit-status '(.metadata | length) == 0 and .status_code == 200'
  query /1.0/images?project=default | jq --exit-status '(.metadata | length) == 0 and .status_code == 200'
  query /1.0/images?recursion=1 | jq --exit-status '(.metadata | length) == 0 and .status_code == 200'
  query /1.0/images?recursion=1\&project=default | jq --exit-status '(.metadata | length) == 0 and .status_code == 200'

  if [ -z "${CERT_NAME:-}" ]; then
    # Untrusted callers see a generic 404 for non-default projects, or a 403 if using "all-projects".
    query /1.0/images?project=foo | jq --exit-status '.error == "Not Found" and .error_code == 404'
    query /1.0/images?recursion=1\&project=foo | jq --exit-status '.error == "Not Found" and .error_code == 404'
    query /1.0/images?project=bar | jq --exit-status '.error == "Not Found" and .error_code == 404'
    query /1.0/images?recursion=1\&project=bar | jq --exit-status '.error == "Not Found" and .error_code == 404'
    query /1.0/images?all-projects=true | jq --exit-status '.error == "Untrusted callers may only access public images in the default project" and .error_code == 403'
    query /1.0/images?recursion=1\&all-projects=true | jq --exit-status '.error == "Untrusted callers may only access public images in the default project" and .error_code == 403'
  elif [ "${CERT_NAME}" = "restricted" ]; then
    # Restricted TLS clients without access to any projects see a 403 for non-default projects, and a 403 if using all-projects.
    query /1.0/images?project=foo | jq --exit-status '.error == "Certificate is restricted" and .error_code == 403'
    query /1.0/images?recursion=1\&project=foo  | jq --exit-status '.error == "Certificate is restricted" and .error_code == 403'
    query /1.0/images?project=bar | jq --exit-status '.error == "Certificate is restricted" and .error_code == 403'
    query /1.0/images?recursion=1\&project=bar  | jq --exit-status '.error == "Certificate is restricted" and .error_code == 403'
    query /1.0/images?all-projects=true  | jq --exit-status '.error == "Certificate is restricted" and .error_code == 403'
    query /1.0/images?recursion=1\&all-projects=true  | jq --exit-status '.error == "Certificate is restricted" and .error_code == 403'
  elif [ "${CERT_NAME}" = "fine-grained" ]; then
    # Fine-grained identities with no permissions see a generic 404 for non-default projects, and a 200 with an empty list if using "all-projects".
    query /1.0/images?project=foo | jq --exit-status '.error == "Not Found" and .error_code == 404'
    query /1.0/images?recursion=1\&project=foo | jq --exit-status '.error == "Not Found" and .error_code == 404'
    query /1.0/images?project=bar | jq --exit-status '.error == "Not Found" and .error_code == 404'
    query /1.0/images?recursion=1\&project=bar | jq --exit-status '.error == "Not Found" and .error_code == 404'
    query /1.0/images?all-projects=true | jq --exit-status '(.metadata | length) == 0 and .status_code == 200'
    query /1.0/images?recursion=1\&all-projects=true | jq --exit-status '(.metadata | length) == 0 and .status_code == 200'
  else
      echo "Unexpected certificate name: ${CERT_NAME}"
      false
  fi

  # All users see a not found error for the aliases.
  query /1.0/images/aliases/default-img | jq --exit-status '.error == "Not Found" and .error_code == 404'
  query /1.0/images/aliases/foo-img?project=foo | jq --exit-status '.error == "Not Found" and .error_code == 404'
  query /1.0/images/aliases/foo-img?project=bar | jq --exit-status '.error == "Not Found" and .error_code == 404'

  # Get the image fingerprint.
  fingerprint="$(lxc query /1.0/images/aliases/foo-img?project=foo | jq --exit-status --raw-output '.target')"

  # All users see a not found error when getting or exporting the image.
  query "/1.0/images/${fingerprint}?project=foo" | jq --exit-status '.error == "Not Found" and .error_code == 404'
  query "/1.0/images/${fingerprint}/export?project=foo" | jq --exit-status '.error == "Not Found" and .error_code == 404'
  query "/1.0/images/${fingerprint}?project=bar" | jq --exit-status '.error == "Not Found" and .error_code == 404'
  query "/1.0/images/${fingerprint}/export?project=bar" | jq --exit-status '.error == "Not Found" and .error_code == 404'

  # No callers can use an invalid secret.
  query /1.0/images/"${fingerprint}"?project=foo\&secret=bar | jq --exit-status '.error == "Not Found" and .error_code == 404'
  query /1.0/images/"${fingerprint}"/export?project=foo\&secret=bar | jq --exit-status '.error == "Not Found" and .error_code == 404'
  query /1.0/images/"${fingerprint}"?project=bar\&secret=bar | jq --exit-status '.error == "Not Found" and .error_code == 404'
  query /1.0/images/"${fingerprint}"/export?project=bar\&secret=bar | jq --exit-status '.error == "Not Found" and .error_code == 404'

  # Get a secret for "default-img" (in the default project).
  secret="$(lxc -X POST query "/1.0/images/${fingerprint}/secret" | jq --exit-status --raw-output '.metadata.secret')"

  # All callers can view the image with a valid secret.
  query /1.0/images/"${fingerprint}"?secret="${secret}" | jq --exit-status '.status_code == 200'

  # All callers can export the image with a valid secret.
  query /1.0/images/"${fingerprint}"/export?secret="${secret}" -o /dev/null

  # Get a secret for "foo-img" (in the foo project).
  secret="$(lxc -X POST query "/1.0/images/${fingerprint}/secret?project=foo" | jq --exit-status --raw-output '.metadata.secret')"

  # All callers can view the image with a valid secret.
  query /1.0/images/"${fingerprint}"?project=foo\&secret="${secret}" | jq --exit-status '.status_code == 200'

  # All callers can export the image with a valid secret.
  query /1.0/images/"${fingerprint}"/export?project=foo\&secret="${secret}" -o /dev/null

  # The secrets do not work 5 seconds after being used.
  sleep 5
  query /1.0/images/"${fingerprint}"?secret="${secret}" | jq --exit-status '.error == "Not Found" and .error_code == 404'
  query /1.0/images/"${fingerprint}"/export?secret="${secret}" | jq --exit-status '.error == "Not Found" and .error_code == 404'
  query /1.0/images/"${fingerprint}"?project=foo\&secret="${secret}" | jq --exit-status '.error == "Not Found" and .error_code == 404'
  query /1.0/images/"${fingerprint}"/export?project=foo\&secret="${secret}" | jq --exit-status '.error == "Not Found" and .error_code == 404'

  # Set the image in the default project to public.
  lxc image show "${fingerprint}" | sed -e "s/public: false/public: true/" | lxc image edit "${fingerprint}"

  # All callers can see the public image when listing.
  query /1.0/images | jq --exit-status '(.metadata | length) == 1 and .status_code == 200'
  query /1.0/images?project=default | jq --exit-status '(.metadata | length) == 1 and .status_code == 200'
  query /1.0/images?recursion=1 | jq --exit-status '(.metadata | length) == 1 and .status_code == 200'
  query /1.0/images?recursion=1\&project=default | jq --exit-status '(.metadata | length) == 1 and .status_code == 200'

  # All callers can view aliases of public images in the default project.
  query /1.0/images/aliases/default-img | jq --exit-status '.status_code == 200'
  query /1.0/images/aliases/default-img?project=default | jq --exit-status '.status_code == 200'

  # All callers can get the image with a prefix of 12 characters or more.
  query "/1.0/images/%25" | jq --exit-status '.error == "Image fingerprint prefix must contain 12 characters or more" and .error_code == 400'
  query "/1.0/images/${fingerprint:0:11}" | jq --exit-status '.error == "Image fingerprint prefix must contain 12 characters or more" and .error_code == 400'
  query "/1.0/images/%25${fingerprint:0:11}" | jq --exit-status '.error == "Failed validating image fingerprint: Hash must contain only lowercase hexadecimal characters" and .error_code == 400'
  query "/1.0/images/${fingerprint}abc" | jq --exit-status '.error == "Image fingerprint cannot be longer than 64 characters" and .error_code == 400'
  query "/1.0/images/${fingerprint:0:12}" | jq --exit-status '.status_code == 200'
  query "/1.0/images/${fingerprint}" | jq --exit-status '.status_code == 200'
  query "/1.0/images/${fingerprint}?project=default" | jq --exit-status '.status_code == 200'

  # Marking an image as public is forbidden when the image is in a non-default project. This is effectively testing the PUT request.
  if err_msg="$(lxc image show "${fingerprint}" --project foo | sed -e "s/public: false/public: true/" | lxc image edit "${fingerprint}" --project foo 2>&1)"; then
    echo "ERROR: Marking a non-default project image as public unexpectedly succeeded" >&2
    exit 1
  fi

  echo "${err_msg}" | grep -Fq "Images can only be marked public in the default project"

  # Ensure that PATCH request also does not allow marking an image as public when the image is in a non-default project.
  if err_msg="$(lxc query -X PATCH "/1.0/images/${fingerprint}?project=foo" -d '{"public": true}' 2>&1)"; then
    echo "ERROR: Marking a non-default project image as public unexpectedly succeeded" >&2
    exit 1
  fi

  echo "${err_msg}" | grep -Fq "Images can only be marked public in the default project"

  # Untrusted callers still cannot access the image in the non-default project.
  if [ -z "${CERT_NAME:-}" ]; then
    query "/1.0/images/aliases/foo-img?project=foo" | jq --exit-status '.error_code == 404'
    query "/1.0/images/${fingerprint}?project=foo" | jq --exit-status '.error_code == 404'
    query "/1.0/images/${fingerprint}/export?project=foo" | jq --exit-status '.error_code == 404'
    # Also assert with an invalid secret to prevent bypasses where any non-empty secret skips non-default restrictions.
    query "/1.0/images/${fingerprint}?project=foo&secret=bar" | jq --exit-status '.error_code == 404'
    query "/1.0/images/${fingerprint}/export?project=foo&secret=bar" | jq --exit-status '.error_code == 404'
  fi

  # All callers can export the public image if using a valid prefix.
  query "/1.0/images/%25/export" | jq --exit-status '.error == "Image fingerprint prefix must contain 12 characters or more" and .error_code == 400'
  query "/1.0/images/${fingerprint:0:11}/export" | jq --exit-status '.error == "Image fingerprint prefix must contain 12 characters or more" and .error_code == 400'
  query "/1.0/images/%25${fingerprint:0:11}/export" | jq --exit-status '.error == "Failed validating image fingerprint: Hash must contain only lowercase hexadecimal characters" and .error_code == 400'
  query "/1.0/images/${fingerprint}abc/export" | jq --exit-status '.error == "Image fingerprint cannot be longer than 64 characters" and .error_code == 400'
  query "/1.0/images/${fingerprint}/export" -o /dev/null
  query "/1.0/images/${fingerprint}/export?project=default" -o /dev/null
  query "/1.0/images/${fingerprint:0:12}/export?project=default" -o /dev/null

  # Clean up.
  lxc image delete "${fingerprint}" --project foo
  lxc project delete foo
  lxc image delete "${fingerprint}"
}

test_image_cached() {
  echo "==> Test that images are being cached consistently between projects."

  if lxc image alias list testimage | grep -wF "testimage"; then
    lxc image delete testimage
  fi

  # Set up a local LXD server as an image remote.
  local LXD2_DIR LXD2_ADDR
  LXD2_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  spawn_lxd "${LXD2_DIR}" true
  LXD2_ADDR=$(< "${LXD2_DIR}/lxd.addr")

  LXD_DIR="${LXD2_DIR}" deps/import-busybox --alias testimage --public

  token="$(LXD_DIR=${LXD2_DIR} lxc config trust add --name foo -q)"
  lxc remote add l2 "${LXD2_ADDR}" --token "${token}"

  echo "==> Verify that image caching works correctly for the default project."

  # 1. Create a new instance in the default project with a remote image that is not stored locally.
  lxc init l2:testimage c1

  local fingerprint
  fingerprint="$(lxc config get c1 volatile.base_image)"

  lxc delete c1

  # 2. Check that the locally saved image is marked as cached.
  lxc image info "${fingerprint}" | grep -xF "Cached: yes"

  # 3. Explicitly copy the same remote image to local storage.
  lxc image copy l2:testimage local:

  # 4. Check that the locally saved image is now marked as non-cached.
  lxc image info "${fingerprint}" | grep -xF "Cached: no"

  lxc image delete "${fingerprint}"

  echo "==> Verify that image caching works correctly for a custom project with features.images=true."

  # 1. Create a new project p1. It has features.images=true by default.
  defaultPoolName="$(lxc profile device get default root pool)"
  lxc project create --storage "${defaultPoolName}" p1

  # 2. Create a new instance in the p1 project with a remote image that is not stored locally.
  lxc init l2:testimage c1 --target-project p1

  lxc delete c1 --project p1

  # 3. Check that the locally saved image is marked as cached.
  lxc image info "${fingerprint}" --project p1 | grep -xF "Cached: yes"

  # 4. Explicitly copy the same remote image to local storage.
  lxc image copy l2:testimage local: --target-project p1

  # 5. Check that the locally saved image is now marked as non-cached.
  lxc image info "${fingerprint}" --project p1 | grep -xF "Cached: no"

  lxc image delete "${fingerprint}" --project p1

  echo "==> Verify that image caching works correctly for a custom project with features.images=false."

  # 1. Set features.images=false for project p1.
  lxc project set p1 features.images=false

  # 2. Create a new instance in the p1 project with a remote image that is not stored locally.
  lxc init l2:testimage c1 --target-project p1

  lxc delete c1 --project p1

  # 3. Check that the locally saved image is marked as cached.
  lxc image info "${fingerprint}" --project p1 | grep -xF "Cached: yes"

  # 4. Explicitly copy the same remote image to local storage.
  lxc image copy l2:testimage local: --target-project p1

  # 5. Check that the locally saved image is now marked as non-cached.
  lxc image info "${fingerprint}" --project p1 | grep -xF "Cached: no"

  lxc image delete "${fingerprint}" --project p1

  echo "==> Verify that image caching works correctly across the projects."

  # 1. Set features.images=true for project p1.
  lxc project set p1 features.images=true

  # 2. Create a new instance in the default project with a remote image that is not stored locally.
  lxc init l2:testimage c1

  lxc delete c1

  # 3. Explicitly copy the same remote image to local storage in project p1.
  lxc image copy l2:testimage local: --target-project p1

  # 4. Check that the locally saved image in the default project is marked as cached.
  lxc image info "${fingerprint}" | grep -xF "Cached: yes"

  # 5. Check that the locally saved image in the p1 project is marked as non-cached.
  lxc image info "${fingerprint}" --project p1 | grep -xF "Cached: no"

  # Clean up.
  lxc image delete "${fingerprint}" --project p1
  lxc image delete "${fingerprint}"
  lxc project delete p1
  lxc remote remove l2
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

test_image_with_exec_output_symlink() {
  local tmpDir opID
  tmpDir=$(mktemp -d -p "${TEST_DIR}" XXX)

  # Export image.
  lxc image export images:alpine/edge "${tmpDir}/image"

  # Unpack image.
  mkdir "${tmpDir}/repack"
  xz -cd "${tmpDir}/image" | tar -f- -vx -C "${tmpDir}/repack"

  # Create symlink for exec-output.
  rm -rf "${tmpDir}/repack/exec-output"
  ln -s "${tmpDir}" "${tmpDir}/repack/exec-output"

  # Repack image.
  tar -cf- -C "${tmpDir}/repack" . | xz -c > "${tmpDir}/image"
  rm -rf "${tmpDir}/repack"

  # Import image.
  lxc image import "${tmpDir}"/* --alias image-repack
  lxc launch image-repack c-repack

  # Exec with record-output via REST API and confirm output is written to symlinked host path.
  opID=$(lxc query -X POST /1.0/instances/c-repack/exec -d '{
    "command":["cat", "/etc/os-release"],
    "record-output":true
  }' | jq --raw-output --exit-status .id)

  op=$(lxc query "/1.0/operations/${opID}")
  echo "${op}" | jq --exit-status '.status == "Failure"'
  echo "${op}" | jq --exit-status '.err | contains("openat exec-output: path escapes from parent")'

  lxc delete -f c-repack
  lxc image delete image-repack

  rm -rf "${tmpDir}"
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
