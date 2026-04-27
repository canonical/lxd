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

  # Phase 1: create a pending public cluster link and capture the certificate it fetched.
  pending="$(lxc query --request POST /1.0/cluster/links --data "{\"name\":\"self-link\",\"type\":\"public\",\"remote_address\":\"${server_addr}\"}")"
  link_cert="$(echo "${pending}" | jq --exit-status '.certificate')"

  # Phase 2: confirm the pending link by resubmitting the fetched certificate.
  lxc query --request POST /1.0/cluster/links --data "{\"name\":\"self-link\",\"type\":\"public\",\"remote_address\":\"${server_addr}\",\"cluster_certificate\":${link_cert}}" > /dev/null

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

  # Name with forward slash.
  if lxc image registry create test/val url=https://example.com 2>/dev/null; then
    echo "ERROR: Create with slash in name unexpectedly succeeded" >&2
    exit 1
  fi

  # Name with colon.
  if lxc image registry create test:val url=https://example.com 2>/dev/null; then
    echo "ERROR: Create with colon in name unexpectedly succeeded" >&2
    exit 1
  fi

  # LXD (cluster) with nonexistent cluster link.
  if lxc image registry create test-val cluster=nonexistent source_project=default 2>/dev/null; then
    echo "ERROR: Create LXD with nonexistent cluster link unexpectedly succeeded" >&2
    lxc image registry delete test-val 2>/dev/null || true
    exit 1
  fi
}
