Here's what's left:

**Needs design work**
1. **`lxd-agent` inside emulated VMs.** The snap only ships an amd64 agent, so `lxc exec` and `lxc file` don't work. `cloud-init.*` user-data and `lxd` also depend on the agent. Fixing this means cross-building `riscv64` and `arm` agents into the snap and injecting the one matching the guest architecture.
2. **Clustering.** Each member needs to record its emulated architectures, which is a cluster schema update. Then:
   - placement, evacuation and `lxc move` without `--target` need to consider them;
   - `ClusterMember` should expose them for `lxc cluster list/show` and placement scriptlets, which needs another API extension.

**Behaviour to verify or fix**
3. **Normal stop on `riscv64`.** With ACPI off, QEMU probably can't send a power-button event to the guest, so `lxc stop` would likely time out. The new `vm` check will confirm it once the snap ships the emulators, and a fix may be needed.
4. **Live migration and stateful stop** of emulated VMs are untested.

**Smaller improvements**
5. **Secure boot default.** Emulated VMs currently fail at start unless you set `boot.mode=uefi-nosecureboot`. Either reject this clearly at creation, or default to no secure boot for these architectures.
6. **Reject unsupported features up front** for emulated VMs:
   - PCI/GPU passthrough;
   - SR-IOV/VDPA NIC acceleration;
   - `security.sev*`;
   - `boot.debug_edk2` on `armv7l`.
7. **Cached-image lookups** in `daemon_images.go:174` and `instance.go:819` should also check emulated architectures for VMs.
8. **Memory overhead.** QEMU's translated-code cache (`tb-size`) isn't limited or counted in `limits.memory`. Cap it or document it.
9. **`riscv64` firmware on amd64** is Ubuntu's stock build, without the snap's EDK2 patches. Consider cross-building it from the pinned EDK2 source.

**Release and CI tasks**
10. **Real snap build.** Build the amd64 snap and record its size increase.
11. **Snap test skip.** Once `latest/edge` ships the emulators, change the `vm` skip from a warning to a failure on x86_64.
12. **Commits.** Sign off, sign and push the unsigned commits. The earliest are the three `vm` and CI commits you already re-signed, which the later ones were built on top of.
