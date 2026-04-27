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
  lxc image registry create test-streams --protocol=simplestreams --description="Test SimpleStreams" url=https://example.com user.note=hello

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

  # Create a public LXD registry.
  lxc image registry create test-lxd --protocol=lxd --description="Test LXD" url=https://lxd.example.com source_project=default

  # Verify it appears in the list.
  registry_show="$(lxc image registry show test-lxd)"
  echo "${registry_show}" | grep -xF "name: test-lxd"
  echo "${registry_show}" | grep -xF "protocol: lxd"
  echo "${registry_show}" | grep -xF "public: true"
  echo "${registry_show}" | grep -xF "builtin: false"
  echo "${registry_show}" | grep -F "url: https://lxd.example.com"
  echo "${registry_show}" | grep -F "source_project: default"

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
  if lxc image registry rename test-streams test-lxd 2>/dev/null; then
    echo "ERROR: Rename to existing name unexpectedly succeeded" >&2
    exit 1
  fi

  sub_test "Edit image registry via stdin"
  # Use show | sed | edit pattern to change description.
  lxc image registry show test-streams | sed 's/description:.*/description: edited via stdin/' | lxc image registry edit test-streams
  [ "$(lxc image registry get test-streams -p description)" = "edited via stdin" ]

  sub_test "Delete image registries"
  lxc image registry delete test-streams
  lxc image registry delete test-lxd

  # Verify deleted image registries are gone.
  ! lxc image registry show test-streams 2>/dev/null || false
  ! lxc image registry show test-lxd 2>/dev/null || false
  ! lxc image registry list --format csv | grep -wF "test-streams" || false
  ! lxc image registry list --format csv | grep -wF "test-lxd" || false

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
  lxc image registry create test-dup --protocol=simplestreams url=https://example.com
  if lxc image registry create test-dup --protocol=simplestreams url=https://example2.com 2>/dev/null; then
    echo "ERROR: Creating duplicate registry unexpectedly succeeded" >&2
    lxc image registry delete test-dup 2>/dev/null || true
    exit 1
  fi

  lxc image registry delete test-dup

  sub_test "Verify image registry validation"
  # Missing protocol.
  if lxc image registry create test-val url=https://example.com 2>/dev/null; then
    echo "ERROR: Create without protocol unexpectedly succeeded" >&2
    lxc image registry delete test-val 2>/dev/null || true
    exit 1
  fi

  # Unknown protocol.
  if lxc image registry create test-val --protocol=unknown url=https://example.com 2>/dev/null; then
    echo "ERROR: Create with unknown protocol unexpectedly succeeded" >&2
    lxc image registry delete test-val 2>/dev/null || true
    exit 1
  fi

  # SimpleStreams without url.
  if lxc image registry create test-val --protocol=simplestreams 2>/dev/null; then
    echo "ERROR: Create SimpleStreams without url unexpectedly succeeded" >&2
    lxc image registry delete test-val 2>/dev/null || true
    exit 1
  fi

  # SimpleStreams with http (not https) url.
  if lxc image registry create test-val --protocol=simplestreams url=http://example.com 2>/dev/null; then
     echo "ERROR: Create SimpleStreams with HTTP (not HTTPS) url unexpectedly succeeded" >&2
    lxc image registry delete test-val 2>/dev/null || true
    exit 1
  fi

  # SimpleStreams with cluster.
  if lxc image registry create test-val --protocol=simplestreams url=https://example.com cluster=foo 2>/dev/null; then
    echo "ERROR: Create SimpleStreams with cluster unexpectedly succeeded" >&2
    lxc image registry delete test-val 2>/dev/null || true
    exit 1
  fi

  # SimpleStreams with source_project.
  if lxc image registry create test-val --protocol=simplestreams url=https://example.com source_project=default 2>/dev/null; then
    echo "ERROR: Create SimpleStreams with source_project unexpectedly succeeded" >&2
    lxc image registry delete test-val 2>/dev/null || true
    exit 1
  fi

  # LXD without source_project.
  if lxc image registry create test-val --protocol=lxd url=https://lxd.example.com 2>/dev/null; then
    echo "ERROR: Create LXD without source_project unexpectedly succeeded" >&2
    lxc image registry delete test-val 2>/dev/null || true
    exit 1
  fi

  # LXD with both url and cluster.
  if lxc image registry create test-val --protocol=lxd url=https://lxd.example.com cluster=foo source_project=default 2>/dev/null; then
    echo "ERROR: Create LXD with both url and cluster unexpectedly succeeded" >&2
    lxc image registry delete test-val 2>/dev/null || true
    exit 1
  fi

  # LXD without url or cluster.
  if lxc image registry create test-val --protocol=lxd source_project=default 2>/dev/null; then
    echo "ERROR: Create LXD without url or cluster unexpectedly succeeded" >&2
    lxc image registry delete test-val 2>/dev/null || true
    exit 1
  fi

  # Invalid URL.
  if lxc image registry create test-val --protocol=simplestreams url=not-a-url 2>/dev/null; then
    echo "ERROR: Create with invalid url unexpectedly succeeded" >&2
    lxc image registry delete test-val 2>/dev/null || true
    exit 1
  fi

  # URL with basic auth credentials.
  if lxc image registry create test-val --protocol=simplestreams url=https://user:pass@example.com 2>/dev/null; then
    echo "ERROR: Create with basic auth url unexpectedly succeeded" >&2
    lxc image registry delete test-val 2>/dev/null || true
    exit 1
  fi

  # Invalid config key.
  if lxc image registry create test-val --protocol=simplestreams url=https://example.com badkey=value 2>/dev/null; then
    echo "ERROR: Create with invalid config key unexpectedly succeeded" >&2
    lxc image registry delete test-val 2>/dev/null || true
    exit 1
  fi

  # Name with forward slash.
  if lxc image registry create test/val --protocol=simplestreams url=https://example.com 2>/dev/null; then
    echo "ERROR: Create with slash in name unexpectedly succeeded" >&2
    exit 1
  fi

  # Name with colon.
  if lxc image registry create test:val --protocol=simplestreams url=https://example.com 2>/dev/null; then
    echo "ERROR: Create with colon in name unexpectedly succeeded" >&2
    exit 1
  fi

  # LXD with nonexistent cluster link.
  if lxc image registry create test-val --protocol=lxd cluster=nonexistent source_project=default 2>/dev/null; then
    echo "ERROR: Create LXD with nonexistent cluster link unexpectedly succeeded" >&2
    lxc image registry delete test-val 2>/dev/null || true
    exit 1
  fi
}
