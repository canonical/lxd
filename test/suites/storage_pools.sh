test_storage_pools() {
  local lxd_backend
  lxd_backend=$(storage_backend "${LXD_DIR}")

  # This test exercises storage pool create/delete code paths for selected
  # specialized storage drivers using placeholder configuration. These drivers
  # normally require dedicated appliances that are not available on GitHub hosted
  # runners, so the test never interacts with a real backend. Because it does
  # not depend on the active local backend, it only needs to run once, hence
  # the restriction to "dir".
  if [ "${lxd_backend}" != "dir" ]; then
    export TEST_UNMET_REQUIREMENT="storage_pools generic test only runs on the dir backend"
    return
  fi

  # The exercised connection modes require specific kernel modules. Ensure the
  # ones shipped by linux-modules-extra are available, installing that package if
  # any is missing. The scsi/fc module is not shipped even by that package, so it
  # is not required here and its PowerStore variant is skipped below when absent.
  local requiredModule installedExtra=0
  for requiredModule in iscsi_tcp nvme_fabrics nvme_tcp; do
    if modprobe --dry-run "${requiredModule}" 2>/dev/null; then
      continue
    fi

    if [ "${installedExtra}" = "0" ]; then
      install_packages "linux-modules-extra-$(uname -r)"
      installedExtra=1
    fi

    if ! modprobe --dry-run "${requiredModule}" 2>/dev/null; then
      export TEST_UNMET_REQUIREMENT="storage_pools generic test requires the ${requiredModule} kernel module"
      return
    fi
  done

  local poolPrefix
  poolPrefix="lxdtest-$(basename "${LXD_DIR}")"

  # PowerStore does not contact the backend during pool creation, so a pool can
  # be created and deleted using placeholder configuration. The only requirement
  # is that the kernel module for the selected connection mode can be loaded.
  sub_test "PowerStore pools can be created and deleted with placeholder config"
  local modeSpec modeName module poolName
  for modeSpec in "iscsi:iscsi_tcp" "nvme/tcp:nvme_tcp" "scsi/fc:scsi_transport_fc"; do
    modeName="${modeSpec%:*}"
    module="${modeSpec#*:}"

    if ! modprobe --dry-run "${module}" 2>/dev/null; then
      echo "==> SKIP: PowerStore ${modeName} mode (kernel module ${module} unavailable)"
      continue
    fi

    poolName="${poolPrefix}-powerstore-${modeName//\//-}"
    lxc storage create "${poolName}" powerstore \
      powerstore.gateway=https://127.0.0.1:1234 \
      powerstore.user.password=secret \
      powerstore.mode="${modeName}"
    lxc storage show "${poolName}"
    lxc storage delete "${poolName}"
    ! lxc storage show "${poolName}" || false
  done

  # The Pure Storage, HPE Alletra and PowerFlex drivers all contact their remote
  # backend while creating a pool (Pure and Alletra provision the pool, PowerFlex
  # discovers the system version). With placeholder configuration pointing at a
  # closed local port, pool creation must fail cleanly and leave no pool behind.
  sub_test "Remote pool creation fails cleanly with placeholder config"

  poolName="${poolPrefix}-pure"
  ! lxc storage create "${poolName}" pure pure.gateway=https://127.0.0.1:1234 pure.api.token=secret pure.mode=iscsi || false
  ! lxc storage show "${poolName}" || false

  poolName="${poolPrefix}-alletra"
  ! lxc storage create "${poolName}" alletra alletra.wsapi=https://127.0.0.1:1234 alletra.user.name=admin alletra.user.password=secret alletra.cpg=fakecpg alletra.mode=iscsi || false
  ! lxc storage show "${poolName}" || false

  poolName="${poolPrefix}-powerflex"
  ! lxc storage create "${poolName}" powerflex powerflex.gateway=https://127.0.0.1:1234 powerflex.user.password=secret powerflex.pool=fakepool powerflex.mode=nvme/tcp || false
  ! lxc storage show "${poolName}" || false
}
