_instance_apparmor() {
  ensure_import_testimage

  echo "Create and start a test container"
  lxc launch testimage c1

  # Clear the ring buffer without flooding the console
  dmesg --clear

  echo "==> Test /sys/kernel/* access"

  echo "Check /sys/kernel/config/"
  echo "Verify read denied"
  ! lxc exec c1 -- ls /sys/kernel/config/ || false

  echo "Check /sys/kernel/debug/"
  echo "Verify read denied"
  ! lxc exec c1 -- ls /sys/kernel/debug/ || false

  echo "Check /sys/kernel/tracing/"
  echo "Verify read denied"
  ! lxc exec c1 -- ls /sys/kernel/tracing/ || false

  echo "Check /sys/kernel/security/"
  echo "Verify read allowed"
  lxc exec c1 -- ls /sys/kernel/security/

  echo "Check /sys/kernel/security/testfile"
  echo "Verify write denied"
  ! lxc exec c1 -- touch /sys/kernel/security/testfile || false

  echo "Check /sys/kernel/security/apparmor/"
  echo "Verify write allowed"
  lxc exec c1 -- touch /sys/kernel/security/apparmor/.load

  echo "==> Test /sys/firmware/efi/efivars/ access"

  if test -d /sys/firmware/efi/efivars/; then
    echo "Verify read denied"
    ! lxc exec c1 -- ls /sys/firmware/efi/efivars/ || false
  else
    echo "    EFI efivars not present (skipping test)"
  fi

  echo "==> Test /proc/sys/kernel access"

  echo "Check /proc/sys/kernel/hostname"
  echo "Verify read allowed"
  lxc exec c1 -- cat /proc/sys/kernel/hostname

  echo "Check /proc/sys/kernel/domainname"
  echo "Verify read allowed"
  lxc exec c1 -- cat /proc/sys/kernel/domainname

  echo "Check /proc/sys/kernel/sem"
  echo "Verify read allowed"
  lxc exec c1 -- cat /proc/sys/kernel/sem

  echo "Check /proc/sys/kernel/shmmax"
  echo "Verify read allowed"
  lxc exec c1 -- cat /proc/sys/kernel/shmmax

  echo "Check /proc/sys/kernel/msgmax"
  echo "Verify read allowed"
  lxc exec c1 -- cat /proc/sys/kernel/msgmax

  echo "Check /proc/sys/kernel/kptr_restrict"
  if lxc exec c1 -- test -f /proc/sys/kernel/kptr_restrict; then
    echo "Verify read allowed"
    lxc exec c1 -- cat /proc/sys/kernel/kptr_restrict

    echo "Verify write denied"
    ! echo 0 | lxc exec c1 -- tee /proc/sys/kernel/kptr_restrict || false
  else
    echo "    kptr_restrict not available (skipping test)"
  fi

  echo "==> Test /proc access"

  echo "Check /proc/stat"
  echo "Verify read allowed"
  lxc exec c1 -- grep -wm1 "^cpu" /proc/stat

  echo "Check /proc/meminfo"
  echo "Verify read allowed"
  lxc exec c1 -- grep -wm1 "^MemTotal:" /proc/meminfo

  echo "Check /proc/uptime"
  echo "Verify read allowed"
  lxc exec c1 -- cat /proc/uptime

  echo "Check /proc/kcore"
  echo "Verify read denied"
  ! lxc exec c1 -- cat /proc/kcore || false

  echo "Check /proc/sysrq-trigger"
  echo "Verify write denied"
  ! echo 1 | lxc exec c1 -- tee /proc/sysrq-trigger || false

  echo "Verify read denied"
  ! lxc exec c1 -- cat /proc/sysrq-trigger || false

  echo "Check /proc/acpi/**"
  echo "Verify read denied"
  ! lxc exec c1 -- cat /proc/acpi/wakeup || false

  echo "==> Test /proc/sys access"

  echo "Check /proc/sys/fs/file-max"
  echo "Verify read allowed"
  lxc exec c1 -- cat /proc/sys/fs/file-max

  echo "Verify write denied"
  ! echo 0 | lxc exec c1 -- tee /proc/sys/fs/file-max || false

  echo "==> Test /proc/sys/net/* access"

  echo "Check /proc/sys/net/ipv4/ip_forward"
  echo "Verify read allowed"
  lxc exec c1 -- cat /proc/sys/net/ipv4/ip_forward

  echo "Verify write allowed"
  echo 1 | lxc exec c1 -- tee /proc/sys/net/ipv4/ip_forward

  echo "==> Test /proc/sys/fs/binfmt_misc access"

  echo "Verify binfmt_misc is supported"
  if lxc info | grep -F 'unpriv_binfmt: "true"'; then
    echo "    binfmt_misc is supported (unpriv_binfmt enabled)"

    echo "Verify mount allowed"
    lxc exec c1 -- mount -t binfmt_misc none /proc/sys/fs/binfmt_misc
  echo "Verify binfmt_misc status is enabled"
    [ "$(lxc exec c1 -- cat /proc/sys/fs/binfmt_misc/status)" = "enabled" ]
  else
    echo "    binfmt_misc not supported (unpriv_binfmt disabled)"
  fi

  echo "dmesg output"
  journalctl --quiet --no-hostname --no-pager --boot=0 --lines=100 --dmesg

  # Cleanup
  lxc delete c1 --force
}

# _apparmor_library_paths checks how the profiles of a daemon render its LD_LIBRARY_PATH entries.
_apparmor_library_paths() {
  local LXD2_DIR profile entry library_path special
  LXD2_DIR="$(mktemp -d -p "${TEST_DIR}" XXX)"

  # Empty entries at the start, in the middle and at the end, plus a relative one and the root directory.
  # The special path exercises quoting and escaping of the rendered rules.
  special='/lxd-test-apparmor/lib [1]{a,b}*?'
  library_path=":.:/:${special}:${LD_LIBRARY_PATH:-}::/lxd-test-apparmor/lib:"
  LD_LIBRARY_PATH="${library_path}" spawn_lxd "${LXD2_DIR}" true

  LXD_DIR="${LXD2_DIR}" ensure_import_testimage
  LXD_DIR="${LXD2_DIR}" lxc launch testimage c1
  LXD_DIR="${LXD2_DIR}" lxc config device add c1 p1 proxy "listen=tcp:127.0.0.1:$(local_tcp_port)" connect=tcp:127.0.0.1:4321

  # The proxy device runs the confined lxd binary, so it can only start if the profile lets it load its libraries.
  profile="${LXD2_DIR}/security/apparmor/profiles/lxd_forkproxy-p1_c1"
  [ -f "${profile}" ]

  sub_test "Empty, relative and root LD_LIBRARY_PATH entries do not render a rule"
  ! grep -E '^[[:space:]]*"?(/+|\./)\*\*' "${profile}" || false

  sub_test "Absolute LD_LIBRARY_PATH entries are allowed"
  while IFS= read -r entry; do
    # Only the absolute entries other than the root directory are expected in the profile.
    [[ "${entry}" == /?* ]] || continue
    # Entries the profile escapes are checked below.
    if [ "${entry}" = "${special}" ]; then
      continue
    fi
    # The profile renders the entry normalized, so it carries no trailing slash.
    entry="${entry%"${entry##*[!/]}"}"
    grep -xF "  \"${entry}/**\" mr," "${profile}"
  done < <(printf '%s\n' "${library_path}" | tr ':' '\n')

  sub_test "LD_LIBRARY_PATH entries with AppArmor special characters are quoted and escaped"
  grep -xF '  "/lxd-test-apparmor/lib \[1\]\{a,b\}\*\?/**" mr,' "${profile}"

  LXD_DIR="${LXD2_DIR}" lxc delete --force c1
  kill_lxd "${LXD2_DIR}"
}

# _apparmor_library_paths_rpath checks that a daemon which finds its libraries through its own RPATH can be confined with LD_LIBRARY_PATH unset.
_apparmor_library_paths_rpath() {
  if ! command -v patchelf >/dev/null 2>&1; then
    echo "==> SKIP: patchelf not available"
    return
  fi

  local rpath profile entry LXD2_DIR
  rpath=""

  # Record the directories the daemon already searches as an RPATH, so that the copy does not need LD_LIBRARY_PATH.
  while IFS= read -r entry; do
    [[ "${entry}" == /?* ]] || continue
    entry="${entry%"${entry##*[!/]}"}"
    rpath="${rpath:+${rpath}:}${entry}"
  done < <(printf '%s\n' "${LD_LIBRARY_PATH:-}" | tr ':' '\n')

  if [ -z "${rpath}" ]; then
    echo "==> SKIP: LD_LIBRARY_PATH names no library directories"
    return
  fi

  LXD2_DIR="$(mktemp -d -p "${TEST_DIR}" XXX)"

  # The daemon under test finds liblxc and dqlite through LD_LIBRARY_PATH, so give a copy of it an RPATH instead.
  cp "$(command -v lxd)" "${LXD2_DIR}/lxd"
  patchelf --set-rpath "${rpath}" "${LXD2_DIR}/lxd"

  # Spawn the copy without LD_LIBRARY_PATH, so it can only find its libraries through its RPATH.
  # A subshell scopes both variables, as saving LD_LIBRARY_PATH would leave it set to an empty string when it started unset.
  (
    unset LD_LIBRARY_PATH
    PATH="${LXD2_DIR}:${PATH}" spawn_lxd "${LXD2_DIR}" true
  )

  LXD_DIR="${LXD2_DIR}" ensure_import_testimage
  LXD_DIR="${LXD2_DIR}" lxc init testimage c1
  LXD_DIR="${LXD2_DIR}" lxc config device add c1 p1 proxy "listen=tcp:127.0.0.1:$(local_tcp_port)" connect=tcp:127.0.0.1:4321

  sub_test "A daemon without LD_LIBRARY_PATH starts a proxy device from the libraries in its RPATH"
  LXD_DIR="${LXD2_DIR}" lxc start c1
  LXD_DIR="${LXD2_DIR}" lxc list c1 --format csv --columns ns | grep -xF 'c1,RUNNING'

  sub_test "The directories in the RPATH are allowed in the profile"
  profile="${LXD2_DIR}/security/apparmor/profiles/lxd_forkproxy-p1_c1"
  [ -f "${profile}" ]
  while IFS= read -r entry; do
    grep -xF "  \"${entry}/**\" mr," "${profile}"
  done < <(printf '%s\n' "${rpath}" | tr ':' '\n')

  LXD_DIR="${LXD2_DIR}" lxc delete --force c1
  kill_lxd "${LXD2_DIR}"
}

# _apparmor_forkproxy_socket_path checks that a unix socket path made of AppArmor special characters is escaped.
_apparmor_forkproxy_socket_path() {
  ensure_import_testimage

  local sockdir profile expected
  sockdir="${TEST_DIR}/p[1]{a,b}*?"
  mkdir "${sockdir}"

  lxc launch testimage c1
  lxc config device add c1 p1 proxy "listen=unix:${sockdir}/sock" connect=tcp:127.0.0.1:4321 bind=host

  sub_test "A unix socket path with AppArmor special characters can be proxied"
  [ -S "${sockdir}/sock" ]

  sub_test "The special characters of the unix socket path are escaped in the profile"
  # shellcheck disable=SC2153
  profile="${LXD_DIR}/security/apparmor/profiles/lxd_forkproxy-p1_c1"
  expected="  \"${TEST_DIR}/p\\[1\\]\\{a,b\\}\\*\\?/sock\" rw,"
  grep -xF "${expected}" "${profile}"

  lxc delete --force c1
  rm -rf "${sockdir}"
}

# _apparmor_rsync_volume_name checks that rsync can copy a volume named with AppArmor special characters.
_apparmor_rsync_volume_name() {
  local pool="apparmor-dir"
  local vol='v[1]{a,b}*?'

  # The dir driver copies volumes with rsync, which runs under its own AppArmor profile.
  lxc storage create "${pool}" dir
  lxc storage volume create "${pool}" "${vol}"

  sub_test "A volume named with AppArmor special characters can be copied with rsync"
  lxc storage volume copy "${pool}/${vol}" "${pool}/copy"
  lxc storage volume show "${pool}" copy

  lxc storage volume delete "${pool}" copy
  lxc storage volume delete "${pool}" "${vol}"
  lxc storage delete "${pool}"
}

test_apparmor() {
  _instance_apparmor
  _apparmor_library_paths
  _apparmor_library_paths_rpath
  _apparmor_forkproxy_socket_path
  _apparmor_rsync_volume_name
}

test_snap_apparmor() {
  _instance_apparmor
}
