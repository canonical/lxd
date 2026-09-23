test_image_registries_basic() {
  sub_test "Verify built-in registries exist"
  # Built-in registries are created during DB initialization from the static remotes list.
  registry_list_csv="$(lxc image registry list --format csv)"
  echo "${registry_list_csv}" | grep "^images,"
  echo "${registry_list_csv}" | grep "^ubuntu,"
  echo "${registry_list_csv}" | grep "^ubuntu-daily,"
  echo "${registry_list_csv}" | grep "^ubuntu-minimal,"
  echo "${registry_list_csv}" | grep "^ubuntu-minimal-daily,"

  # Verify a built-in registry has the expected properties.
  registry_show="$(lxc image registry show images)"
  echo "${registry_show}" | grep -xF "name: images"
  echo "${registry_show}" | grep -xF "protocol: simplestreams"
  echo "${registry_show}" | grep -xF "public: true"
  echo "${registry_show}" | grep -xF "builtin: true"
  echo "${registry_show}" | grep -F "url: https://images.lxd.canonical.com"

  sub_test "Create and inspect image registries"
  # Create a SimpleStreams registry.
  lxc image registry create test-streams --description="Test SimpleStreams" url=https://example.com user.note=hello

  # Verify it appears in the list.
  lxc image registry list --format csv | grep -wF "test-streams"

  # Verify show output.
  registry_show="$(lxc image registry show test-streams)"
  echo "${registry_show}" | grep -xF "name: test-streams"
  echo "${registry_show}" | grep -xF "description: Test SimpleStreams"
  echo "${registry_show}" | grep -xF "protocol: simplestreams"
  echo "${registry_show}" | grep -xF "public: true"
  echo "${registry_show}" | grep -xF "builtin: false"
  echo "${registry_show}" | grep -F "url: https://example.com"
  echo "${registry_show}" | grep -F "user.note: hello"

  sub_test "Create and use a public LXD image registry via a public cluster link"
  # A public cluster link makes this a public LXD image registry. Point one at this server's
  # own API address to exercise the registry end to end on a standalone server, avoiding the
  # need to stand up a second LXD deployment to link to.
  lxc config set core.https_address "127.0.0.1:$(local_tcp_port)"
  server_addr="$(lxc config get core.https_address)"

  # Make a public image available so the registry has something to list.
  ensure_import_testimage
  lxc image show testimage | sed 's/^public:.*/public: true/' | lxc image edit testimage
  testimage_fingerprint="$(lxc image info testimage | awk '/^Fingerprint:/ {print $2}')"

  # Create a public cluster link that points back at this same server, so the registry can be
  # exercised end to end on a standalone server without standing up a second LXD deployment.
  create_public_cluster_link self-link "${server_addr}"

  # Create a public LXD image registry backed by the public cluster link.
  lxc image registry create test-lxd-public --description="Test public LXD" cluster=self-link source_project=default

  # The registry is reported as public because its cluster link is public.
  registry_show="$(lxc image registry show test-lxd-public)"
  echo "${registry_show}" | grep -xF "name: test-lxd-public"
  echo "${registry_show}" | grep -xF "protocol: lxd"
  echo "${registry_show}" | grep -xF "public: true"
  echo "${registry_show}" | grep -xF "builtin: false"
  echo "${registry_show}" | grep -F "cluster: self-link"
  echo "${registry_show}" | grep -F "source_project: default"

  # Listing images through the registry connects via the public cluster link and returns the public image.
  # LXD_DIR is provided by the test harness; the disable silences a false near-miss against LXD2_DIR below.
  # shellcheck disable=SC2153
  registry_images="$(curl --silent --unix-socket "${LXD_DIR}/unix.socket" "lxd/1.0/image-registries/test-lxd-public/images")"
  echo "${registry_images}" | jq --exit-status --arg fp "${testimage_fingerprint}" '.metadata | any(.fingerprint == $fp)'

  sub_test "Verify list output formats"
  for format in csv json yaml table compact; do
    lxc image registry list --format "${format}" | grep -wF "test-streams"
  done

  sub_test "Get, set, and unset config keys"
  # Get existing config.
  [ "$(lxc image registry get test-streams url)" = "https://example.com" ]
  [ "$(lxc image registry get test-streams user.note)" = "hello" ]

  # Set a new user config key.
  lxc image registry set test-streams user.foo=bar
  [ "$(lxc image registry get test-streams user.foo)" = "bar" ]

  # Unset the user config key.
  lxc image registry unset test-streams user.foo
  [ "$(lxc image registry get test-streams user.foo || echo fail)" = "" ]

  # Set and get description as a property.
  lxc image registry set test-streams -p description="updated desc"
  [ "$(lxc image registry get test-streams -p description)" = "updated desc" ]

  sub_test "Rename image registry"
  lxc image registry rename test-streams test-streams-renamed
  ! lxc image registry show test-streams 2>/dev/null || false
  lxc image registry show test-streams-renamed

  # Rename back.
  lxc image registry rename test-streams-renamed test-streams
  lxc image registry show test-streams

  sub_test "Rename to existing name is rejected"
  if lxc image registry rename test-streams test-lxd-public 2>/dev/null; then
    echo "ERROR: Rename to existing name unexpectedly succeeded" >&2
    exit 1
  fi

  sub_test "Edit image registry via stdin"
  # Use show | sed | edit pattern to change description.
  lxc image registry show test-streams | sed 's/description:.*/description: edited via stdin/' | lxc image registry edit test-streams
  [ "$(lxc image registry get test-streams -p description)" = "edited via stdin" ]

  sub_test "Delete image registries"
  lxc image registry delete test-streams
  lxc image registry delete test-lxd-public

  # Verify deleted image registries are gone.
  ! lxc image registry show test-streams 2>/dev/null || false
  ! lxc image registry show test-lxd-public 2>/dev/null || false
  ! lxc image registry list --format csv | grep -wF "test-streams" || false
  ! lxc image registry list --format csv | grep -wF "test-lxd-public" || false

  # Clean up the public cluster link used by the LXD image registry.
  lxc cluster link delete self-link

  # Restore the original listen address so later tests that connect over the network
  # via ${LXD_ADDR} still reach the daemon.
  lxc config set core.https_address "${LXD_ADDR}"

  sub_test "Verify built-in registries cannot be renamed"
  if lxc image registry rename images test-renamed 2>/dev/null; then
    echo "ERROR: Renaming built-in registry unexpectedly succeeded" >&2
    exit 1
  fi

  sub_test "Verify built-in registries cannot be deleted"
  if lxc image registry delete images 2>/dev/null; then
    echo "ERROR: Deleting built-in registry unexpectedly succeeded" >&2
    exit 1
  fi

  sub_test "Verify built-in registries cannot be updated"
  if lxc image registry set images user.foo=bar 2>/dev/null; then
    echo "ERROR: Updating built-in registry unexpectedly succeeded" >&2
    exit 1
  fi

  sub_test "Verify duplicate name is rejected on create"
  lxc image registry create test-dup url=https://example.com
  if lxc image registry create test-dup url=https://example2.com 2>/dev/null; then
    echo "ERROR: Creating duplicate registry unexpectedly succeeded" >&2
    lxc image registry delete test-dup 2>/dev/null || true
    exit 1
  fi

  lxc image registry delete test-dup

  sub_test "Verify image registry validation"
  # Neither url nor cluster, so no protocol can be inferred.
  if lxc image registry create test-val 2>/dev/null; then
    echo "ERROR: Create without url or cluster unexpectedly succeeded" >&2
    lxc image registry delete test-val 2>/dev/null || true
    exit 1
  fi

  # Only source_project, so no protocol can be inferred.
  if lxc image registry create test-val source_project=default 2>/dev/null; then
    echo "ERROR: Create with only source_project unexpectedly succeeded" >&2
    lxc image registry delete test-val 2>/dev/null || true
    exit 1
  fi

  # Both url and cluster, so the protocol is ambiguous.
  if lxc image registry create test-val url=https://example.com cluster=foo 2>/dev/null; then
    echo "ERROR: Create with both url and cluster unexpectedly succeeded" >&2
    lxc image registry delete test-val 2>/dev/null || true
    exit 1
  fi

  # SimpleStreams (url) with http (not https) url.
  if lxc image registry create test-val url=http://example.com 2>/dev/null; then
    echo "ERROR: Create SimpleStreams with HTTP (not HTTPS) url unexpectedly succeeded" >&2
    lxc image registry delete test-val 2>/dev/null || true
    exit 1
  fi

  # SimpleStreams (url) with source_project.
  if lxc image registry create test-val url=https://example.com source_project=default 2>/dev/null; then
    echo "ERROR: Create SimpleStreams with source_project unexpectedly succeeded" >&2
    lxc image registry delete test-val 2>/dev/null || true
    exit 1
  fi

  # LXD (cluster) without source_project.
  if lxc image registry create test-val cluster=foo 2>/dev/null; then
    echo "ERROR: Create LXD without source_project unexpectedly succeeded" >&2
    lxc image registry delete test-val 2>/dev/null || true
    exit 1
  fi

  # Invalid URL.
  if lxc image registry create test-val url=not-a-url 2>/dev/null; then
    echo "ERROR: Create with invalid url unexpectedly succeeded" >&2
    lxc image registry delete test-val 2>/dev/null || true
    exit 1
  fi

  # URL with basic auth credentials.
  if lxc image registry create test-val url=https://user:pass@example.com 2>/dev/null; then
    echo "ERROR: Create with basic auth url unexpectedly succeeded" >&2
    lxc image registry delete test-val 2>/dev/null || true
    exit 1
  fi

  # Invalid config key.
  if lxc image registry create test-val url=https://example.com badkey=value 2>/dev/null; then
    echo "ERROR: Create with invalid config key unexpectedly succeeded" >&2
    lxc image registry delete test-val 2>/dev/null || true
    exit 1
  fi

  # LXD (cluster) with nonexistent cluster link.
  if lxc image registry create test-val cluster=nonexistent source_project=default 2>/dev/null; then
    echo "ERROR: Create LXD with nonexistent cluster link unexpectedly succeeded" >&2
    lxc image registry delete test-val 2>/dev/null || true
    exit 1
  fi
}

test_image_registries_list_images_compression() {
  local url="https://images.lxd.canonical.com/"
  if ! curl --head --silent "${url}" > /dev/null; then
    export TEST_UNMET_REQUIREMENT="No connectivity to ${url}"
    return
  fi

  registryName="images"

  sub_test "List images from a built-in registry via CLI"
  # The "images" registry points at the public SimpleStreams server.
  # Verify the command succeeds and returns at least one image.
  image_list="$(lxc image list --registry "${registryName}" --format csv)"
  [ -n "${image_list}" ]

  sub_test "Verify API endpoint gzip compression reduces response size"
  # Fetch both responses, saving the body and recording the download size.
  uncompressed_file="$(mktemp -p "${TEST_DIR}" XXX)"
  compressed_file="$(mktemp -p "${TEST_DIR}" XXX)"
  uncompressed_size="$(curl --silent --unix-socket "${LXD_DIR}/unix.socket" -o "${uncompressed_file}" -w '%{size_download}' "lxd/1.0/image-registries/${registryName}/images")"
  compressed_size="$(curl --silent --unix-socket "${LXD_DIR}/unix.socket" -H 'Accept-Encoding: gzip' -o "${compressed_file}" -w '%{size_download}' "lxd/1.0/image-registries/${registryName}/images")"

  # Both responses must be non-empty.
  [ "${uncompressed_size}" -gt 0 ]
  [ "${compressed_size}" -gt 0 ]

  # The compressed response must be smaller than the uncompressed one.
  [ "${compressed_size}" -lt "${uncompressed_size}" ]

  sub_test "Verify compressed and uncompressed responses return the same data"
  # Verify the uncompressed file contains valid non-empty JSON.
  jq --exit-status '.metadata | length > 0' < "${uncompressed_file}"

  # Parse and sort both responses, verifying valid JSON, then compare.
  diff -u <(jq --exit-status -S . "${uncompressed_file}") <(zcat "${compressed_file}" | jq --exit-status -S .)
  rm "${uncompressed_file}" "${compressed_file}"
}

test_image_registries_download() {
  # Exercise downloading images through an LXD image registry backed by an authenticated
  # (unidirectional) cluster link, and verify alias handling on copy.
  #
  # Spawn a second LXD to act as the remote image host. A unidirectional cluster link authenticates
  # to it using a cluster-link identity, which makes the resulting image registry private.
  local LXD2_DIR
  LXD2_DIR="$(mktemp -d -p "${TEST_DIR}" XXX)"
  spawn_lxd "${LXD2_DIR}" true

  # Import a public test image with an extra alias on the remote host.
  LXD_DIR="${LXD2_DIR}" deps/import-busybox --alias testimage --public
  fingerprint="$(LXD_DIR="${LXD2_DIR}" lxc image info testimage | awk '/^Fingerprint/ {print $2}')"
  LXD_DIR="${LXD2_DIR}" lxc image alias create testimage-extra "${fingerprint}"

  sub_test "Create a private LXD image registry via a unidirectional cluster link"
  # On the remote host, grant a cluster-link identity permission to view images, then issue its token.
  LXD_DIR="${LXD2_DIR}" lxc auth group create img-viewers
  LXD_DIR="${LXD2_DIR}" lxc auth group permission add img-viewers project default can_view
  LXD_DIR="${LXD2_DIR}" lxc auth group permission add img-viewers project default can_view_images
  link_token="$(LXD_DIR="${LXD2_DIR}" lxc auth identity create cluster-link/registry-downloader --group img-viewers --quiet)"

  # On this server, consume the token to create a unidirectional link to the remote host.
  lxc cluster link create private-link --token "${link_token}" --unidirectional
  lxc cluster link show private-link | grep -xF 'type: unidirectional'

  lxc image registry create test-lxd-private cluster=private-link source_project=default

  # The registry is reported as private because its cluster link is not public.
  registry_show="$(lxc image registry show test-lxd-private)"
  echo "${registry_show}" | grep -xF "protocol: lxd"
  echo "${registry_show}" | grep -xF "public: false"

  sub_test "Download an image from a private LXD image registry"
  lxc project create download-target
  lxc image copy test-lxd-private:testimage local: --alias private-copy --target-project download-target

  # The image now exists locally in the target project with the requested alias and was downloaded.
  lxc image info private-copy --project download-target | grep -F "Fingerprint: ${fingerprint}"
  lxc image alias list --project download-target --format csv | grep -wF "private-copy"
  stat --terse "${LXD_DIR}/images/${fingerprint}"
  lxc image delete private-copy --project download-target

  sub_test "Copy image aliases from a registry with --copy-aliases"
  # --copy-aliases brings the source image's aliases across.
  lxc image copy test-lxd-private:testimage local: --copy-aliases --target-project download-target
  lxc image alias list --project download-target --format csv | grep -wF "testimage"
  lxc image alias list --project download-target --format csv | grep -wF "testimage-extra"
  # Deleting the image removes its aliases too, leaving a clean slate for the next case.
  lxc image delete testimage --project download-target

  sub_test "Without --copy-aliases only the user-provided alias is applied"
  lxc image copy test-lxd-private:testimage local: --alias user-only --target-project download-target
  lxc image alias list --project download-target --format csv | grep -wF "user-only"
  if lxc image alias list --project download-target --format csv | grep -wF "testimage-extra"; then
    echo "ERROR: source alias unexpectedly copied without --copy-aliases" >&2
    exit 1
  fi
  lxc image delete user-only --project download-target

  sub_test "A user alias and --copy-aliases coexist"
  lxc image copy test-lxd-private:testimage local: --alias user-plus --copy-aliases --target-project download-target
  lxc image alias list --project download-target --format csv | grep -wF "user-plus"
  lxc image alias list --project download-target --format csv | grep -wF "testimage"
  lxc image alias list --project download-target --format csv | grep -wF "testimage-extra"
  lxc image delete user-plus --project download-target

  sub_test "Clean up"
  lxc project delete download-target
  lxc image registry delete test-lxd-private
  lxc cluster link delete private-link
  LXD_DIR="${LXD2_DIR}" lxc auth identity delete cluster-link/registry-downloader
  kill_lxd "${LXD2_DIR}"
}
