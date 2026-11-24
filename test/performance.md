# LXD Integration Test Performance and Operation Cost Guide

This document estimates the **relative runtime cost** of operations commonly used in LXD's integration test suites so test authors can pick the cheapest primitive that validates a given behavior and spot optimization opportunities in existing tests.

Measurements combine:

- **Real GitHub Actions CI timings** from green `main` runs of [`.github/workflows/tests.yml`](../.github/workflows/tests.yml) (`ubuntu-26.04` runners, scheduled with `fast-backend=dir` and push with `fast-backend=btrfs`, November 2026).
- **Controlled local dev VM micro-benchmarks** collected with [`./benchmark-ops.sh`](./benchmark-ops.sh) on 4 vCPU / 5.7 GiB Ubuntu 26.04 QEMU dev VMs (`lxd-test` and `lxd-snap-test`).

Focus on the **relative cost multipliers** rather than exact milliseconds; absolute timings scale with runner hardware, CPU contention, and kernel version, whereas the ratios between operations and backends remain consistent.

---

## 1. Quick Decision Guide

Use the cheapest primitive that exercises the code path under test:

| Goal | Prefer | Avoid | Typical Speedup |
| :--- | :--- | :--- | ---: |
| Verify test is storage-driver agnostic | Place in `standalone`, `cluster`, `network`, `image`, or `instance` (`fast` / `fasts`) | Adding to `standalone_storage`, `cluster_storage`, or `replicator_storage` (`all` backends) | **6×–15×** CI wall-clock (avoids Ceph/ZFS/LVM) |
| Test config, profile, project, or API validation | `lxc init --empty` | `lxc init testimage` or `lxc launch testimage` | **2×–9×** faster |
| Test stopped instance with a rootfs (copy, snapshot, move, export) | `lxc init testimage` | `lxc launch testimage` | **2×–6×** faster |
| Stop an instance (unless testing graceful shutdown) | `lxc stop -f` / `lxc delete -f` | `lxc stop` (graceful) | **8×–14×** faster (`~170ms` vs `~2.2s`) |
| Check instance names or status in assertions/loops | `lxc query /1.0/instances` or `lxc list -f csv -c n` / `-c ns` | `lxc list` (default table) or `recursion=2` | **2×–16×** faster (biggest win in clusters) |
| Create a secondary `lvm`/`ceph` pool in a test | `lxc storage create <p> <drv> volume.size="${DEFAULT_VOLUME_SIZE}"` | Omitting `volume.size` (defaults to `10GiB`) | **~4.4×** faster `init --empty` on LVM |
| Create an empty VM in `system-tests` | `lxc init --empty --vm -c limits.memory=128MiB -d "${SMALL_ROOT_DISK}"` | Omitting `-d "${SMALL_ROOT_DISK}"` (10 GiB block vol) | Avoids 10 GiB allocation / non-thin failures |
| Wait for a snap-test instance to accept `lxc exec` | `waitInstanceReady` | `waitInstanceBooted` (unless testing full systemd/cloud-init boot) | **~3–4s** saved per container, **~7–11s** per VM |
| Choose a backend for VM-heavy storage-agnostic tests | `zfs` (ZVol + compression) | `btrfs` (CoW issues with `.raw` VM disks), `dir`, or non-thin `lvm` | **5×–150×** faster VM snapshots/restores/copies than `dir`/non-thin `lvm` |
| Avoid extra daemon/cluster startup | Reuse the harness daemon where state is isolated | Unnecessary `spawn_lxd` (`~1.1–5.6s`) or 3-node cluster (`~5.6s`) | **1–6s** saved per test |

### Avoid Split Multi-Call Patterns (Minimize Back-and-Forth)

Unless a test specifically exercises the intermediate state or individual subcommand, combine sequential `lxc` calls into a single invocation. Every separate `lxc` call adds CLI process startup, socket/TLS handshake, API round-trips, and (for writes) separate database transactions or Raft commits:

| Avoid (split calls) | Prefer (combined call) | Why it is faster |
| :--- | :--- | :--- |
| `lxc init ... c1` + `lxc start c1` | `lxc launch ... c1` | One CLI invocation; avoids a separate start round-trip |
| `lxc init ... c1` + `lxc config set` / `profile add` / `network attach` (+ `lxc start`) | `lxc init` / `lxc launch` with `-c k=v`, `-d dev,k=v`, `-p prof`, `-n net`, `-s pool` | Applies config, profiles, devices, and network in the creation API call instead of N follow-up `PUT`/`PATCH` calls |
| `lxc stop [-f] c1` + `lxc delete c1` | `lxc delete --force c1` | Stops and deletes in one call (and avoids accidental graceful `lxc stop` `~2.2s` wait) |
| `lxc stop [-f] c1` + `lxc start c1` | `lxc restart [--force] c1` | Single state-change operation (pass `--force` unless testing graceful reboot) |
| `lxc start c1; lxc start c2` (or `stop`, `restart`, `delete`, `image delete`, `file delete`) | `lxc delete -f c1 c2` (or `start`/`stop -f`/`restart -f c1 c2`) | Variadic commands reuse one client session and dispatch instance actions concurrently |
| `lxc config set c1 k1=v1` + `lxc config set c1 k2=v2` *(also `profile`, `project`, `network`, `storage volume`, `cluster`, `config device`, `network acl/zone`)* | `lxc config set c1 k1=v1 k2=v2` | Single DB write / Raft commit instead of one per key |
| `lxc project create p1` + `lxc project set p1 ...` *(also `network`, `storage`, `storage volume`, `config device add`)* | `lxc project create p1 -c k1=v1 -c k2=v2` (or `lxc network/storage create ... k1=v1 k2=v2`; YAML via `< config.yaml` for profiles/projects) | Sets initial config atomically during creation |
| `lxc copy c1 c2` + `lxc delete c1` | `lxc move c1 c2` (supports `-c`, `-d`, `-p`, `-s`, `--target-project`) | Avoids full data copy when moving within a pool and skips a separate delete |
| `lxc cluster role add n1 r1` + `lxc cluster role add n1 r2` | `lxc cluster role add n1 r1,r2` | Single cluster member update |
| `lxc project switch p1` + `lxc <cmd>` + `lxc project switch default` | `lxc --project p1 <cmd>` | Saves two extra CLI calls and avoids leaving the client switched on failure |
| Many consecutive `lxc exec c1 -- <trivial check>` calls | Single `lxc exec c1 -- sh -c 'cmd1 && cmd2'` (when testing related guest state) | Saves `~30–40ms` per skipped exec in `system-tests` and `~110–170ms` in `snap-tests` |
| Repeated `lxc list` / `lxc info` / `lxc query` calls for different fields of the same object | Capture once (`out="$(lxc query ...)"` or `lxc list -f csv -c n,s,L`) and inspect locally | Avoids repeating state/cluster fan-out queries |

### Storage Backend Multipliers at a Glance

| Workload | `btrfs` | `ceph` | `dir` | `lvm` (non-thin) | `lvm` (thin) | `zfs` |
| :--- | ---: | ---: | ---: | ---: | ---: | ---: |
| `system-tests` (`standalone_storage`, 55 common CI tests, `LXD_TMPFS=1`) | **1.01×** | **6.35×** | **1.00×** | — | **2.25×** | **2.49×** |
| `system-tests` (`cluster_storage`, CI total) | **1.42×** | **3.53×** | **1.00×** | — | **1.29×*** | **1.77×** |
| `system-tests` (`replicator_storage`, CI total) | **1.26×*** | **7.12×** | **1.00×** | — | **1.89×** | **1.45×** |
| `snap-tests` (`storage-vm` real 3.5 GiB VMs, CI job total) | **1.00×** | **1.97×** | **1.47×** | **1.59×** | **1.67×** | **1.15×** |

*\* `cluster_storage` skips `clustering_live_migration` on `lvm` and `clustering_storage_block_tracking_vm` on `dir`; `replicator_storage` skips `clustering_replicator_vm` on `dir` and `lvm`.*

> **Container vs. VM backend choice:** For small busybox containers (`test/main.sh`), `btrfs` and `dir` are tied for fastest (`1.0×`). For real multi-GiB VMs (`test/snap/`), `zfs` is the recommended backend: ZVol block devices with compression avoid the CoW issues `btrfs` suffers with `.raw` VM disk files while still providing fast CoW snapshots, restores, and clones (unlike `dir`, which must copy entire multi-GiB `.raw` files).

---

## 2. `system-tests` (`test/main.sh`): Storage-Dependent Operations

Measured with `./test/benchmark-ops.sh --mode system --tmpfs` (matching CI's `LXD_TMPFS=1`, using the cached busybox `testimage`, `DEFAULT_VOLUME_SIZE=24MiB`, and aligned `SMALL_ROOT_DISK=root,size=24MiB`).

The parenthesized multiplier (e.g., `(31×)`) is normalized against `lxc init --empty` on `dir` (`35ms = 1.0×`). The **Ceph / `dir`** column highlights how sharply Ceph amplifies storage-heavy operations.

| Operation | `btrfs` | `ceph` | `dir` | `lvm` (non-thin) | `lvm` (thin) | `zfs` | Ceph / `dir` |
| :--- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| **Daemon & Pool Setup** |  |  |  |  |  |  |  |
| `spawn_lxd` (daemon only) | 1.10s (31×) | 1.09s (31×) | 1.11s (32×) | 1.12s (32×) | 1.07s (31×) | 1.10s (31×) | 1.0× |
| `lxc storage create` (default pool) | 78ms (2.2×) | 3.92s (112×) | 51ms (1.5×) | 258ms (7.4×) | 483ms (14×) | 320ms (9.1×) | **77×** |
| `spawn_lxd` (daemon + pool) | 1.17s (34×) | 5.01s (143×) | 1.16s (33×) | 1.38s (39×) | 1.56s (44×) | 1.42s (40×) | **4.3×** |
| `lxc storage create` (secondary) | 65ms (1.9×) | 3.74s (107×) | 29ms (0.8×) | 224ms (6.4×) | 376ms (11×) | 226ms (6.5×) | **129×** |
| `lxc storage delete` (secondary) | 33ms (0.9×) | 913ms (26×) | 26ms (0.7×) | 157ms (4.5×) | 213ms (6.1×) | 53ms (1.5×) | **35×** |
| `ensure_import_testimage` (cached) | 46ms (1.3×) | 122ms (3.5×) | 50ms (1.4×) | 50ms (1.4×) | 44ms (1.3×) | 39ms (1.1×) | 2.4× |
| `shutdown_lxd` | 215ms (6.1×) | 187ms (5.3×) | 188ms (5.4×) | 172ms (4.9×) | 208ms (5.9×) | 192ms (5.5×) | 1.0× |
| **Container Lifecycle (busybox)** |  |  |  |  |  |  |  |
| `lxc init --empty` | 31ms (0.9×) | 2.07s (59×) | 35ms (**1.0×**) | 287ms (8.2×) | 343ms (9.8×) | 86ms (2.5×) | **59×** |
| `lxc init --empty -d "${SMALL_ROOT_DISK}"` | 33ms (0.9×) | 2.19s (63×) | 34ms (1.0×) | 303ms (8.7×) | 353ms (10×) | 86ms (2.5×) | **64×** |
| `lxc init testimage` | 51ms (1.5×) | 2.76s (79×) | 91ms (2.6×) | 345ms (9.9×) | 405ms (12×) | 98ms (2.8×) | **30×** |
| `lxc init testimage -d "${SMALL_ROOT_DISK}"` | 42ms (1.2×) | 2.73s (78×) | 134ms (3.8×) | 342ms (9.8×) | 464ms (13×) | 97ms (2.8×) | **20×** |
| `lxc launch testimage` | 253ms (7.2×) | 3.03s (87×) | 340ms (9.7×) | 661ms (19×) | 690ms (20×) | 333ms (9.5×) | **8.9×** |
| `lxc launch testimage -d "${SMALL_ROOT_DISK}"` | 258ms (7.4×) | 3.03s (87×) | 298ms (8.5×) | 649ms (19×) | 690ms (20×) | 314ms (9.0×) | **10×** |
| `lxc start` (pre-created testimage) | 164ms (4.7×) | 292ms (8.3×) | 163ms (4.7×) | 198ms (5.7×) | 220ms (6.3×) | 151ms (4.3×) | 1.8× |
| `lxc exec -- true` | 30ms (0.9×) | 34ms (1.0×) | 36ms (1.0×) | 33ms (0.9×) | 32ms (0.9×) | 29ms (0.8×) | 0.9× |
| `lxc restart -f` | 290ms (8.3×) | 1.08s (31×) | 312ms (8.9×) | 428ms (12×) | 429ms (12×) | 299ms (8.5×) | 3.5× |
| `lxc stop -f` | 158ms (4.5×) | 789ms (23×) | 161ms (4.6×) | 254ms (7.3×) | 257ms (7.3×) | 171ms (4.9×) | 4.9× |
| `lxc stop` (graceful, no `-f`) | 2.24s (64×) | 2.82s (81×) | 2.21s (63×) | 2.30s (66×) | 2.33s (67×) | 2.25s (64×) | 1.3× |
| `lxc delete` (stopped) | 29ms (0.8×) | 798ms (23×) | 29ms (0.8×) | 138ms (3.9×) | 140ms (4.0×) | 48ms (1.4×) | **28×** |
| `lxc delete -f` (running) | 211ms (6.0×) | 1.60s (46×) | 202ms (5.8×) | 394ms (11×) | 394ms (11×) | 231ms (6.6×) | 7.9× |
| **Empty VMs (`--vm`)** |  |  |  |  |  |  |  |
| `lxc init --empty --vm -d "${SMALL_ROOT_DISK}"` | 37ms (1.1×) | 2.83s (81×) | 32ms (0.9×) | 447ms (13×) | 525ms (15×) | 133ms (3.8×) | **88×** |
| `lxc init --empty --vm` (default 10 GiB) | 32ms (0.9×) | 2.99s (85×) | 29ms (0.8×) | N/A* | 506ms (14×) | 118ms (3.4×) | **103×** |
| `lxc launch --empty --vm -d "${SMALL_ROOT_DISK}"` | 206ms (5.9×) | 3.44s (98×) | 192ms (5.5×) | 694ms (20×) | 810ms (23×) | 344ms (9.8×) | **18×** |
| **Snapshots, Copy, Backup, Publish & Volumes** |  |  |  |  |  |  |  |
| `lxc snapshot` | 34ms (1.0×) | 1.55s (44×) | 127ms (3.6×) | 275ms (7.9×) | 198ms (5.7×) | 56ms (1.6×) | **12×** |
| `lxc restore` | 38ms (1.1×) | 1.46s (42×) | 100ms (2.9×) | **11.0s (314×)** | 286ms (8.2×) | 66ms (1.9×) | **15×** |
| `lxc copy` (same pool) | 60ms (1.7×) | 3.49s (100×) | 300ms (8.6×) | 1.12s (32×) | 520ms (15×) | 169ms (4.8×) | **12×** |
| `lxc copy --refresh` | 61ms (1.7×) | 2.92s (83×) | 125ms (3.6×) | 740ms (21×) | 605ms (17×) | 142ms (4.1×) | **23×** |
| `lxc export` | 174ms (5.0×) | 885ms (25×) | 160ms (4.6×) | 247ms (7.1×) | 283ms (8.1×) | 188ms (5.4×) | 5.5× |
| `lxc export --optimized-storage` | 182ms (5.2×) | — | — | — | — | 190ms (5.4×) | — |
| `lxc import` | 152ms (4.3×) | 3.22s (92×) | 155ms (4.4×) | 498ms (14×) | 576ms (16×) | 276ms (7.9×) | **21×** |
| `lxc publish` | 186ms (5.3×) | 883ms (25×) | 169ms (4.8×) | 242ms (6.9×) | 252ms (7.2×) | 202ms (5.8×) | 5.2× |
| `lxc storage volume create` (fs, 32 MiB) | 34ms (1.0×) | 1.62s (46×) | 22ms (0.6×) | 164ms (4.7×) | 180ms (5.1×) | 54ms (1.5×) | **74×** |
| `lxc storage volume create --type=block` (32 MiB) | 26ms (0.7×) | 704ms (20×) | 20ms (0.6×) | 91ms (2.6×) | 110ms (3.1×) | 550ms (16×) | **35×** |

*\* Non-thin LVM cannot allocate a default 10 GiB VM disk inside the harness's 7 GiB `DEFAULT_POOL_SIZE`.*

### Key Takeaways from Storage-Dependent Measurements

1. **Keep `SMALL_ROOT_DISK` aligned with `DEFAULT_VOLUME_SIZE` (`24MiB`), and set `volume.size` on secondary pools:**
   - On a **secondary pool** created without `volume.size` (e.g., `lxc storage create p2 lvm`), LXD defaults `volume.size` to **10 GiB**. Creating a container there without `-d "${SMALL_ROOT_DISK}"` takes **1.58s vs. 359ms (4.4× slower)** on LVM thin just to format ext4 on a 10 GiB block volume—and once `testimage` is unpacked into a 10 GiB cached image volume, subsequent `-d "${SMALL_ROOT_DISK}"` calls fail because LXD refuses to shrink an image volume. Always pass `volume.size="${DEFAULT_VOLUME_SIZE}"` when creating secondary `lvm`/`ceph` pools.
   - On the **default harness pool**, `lvm_configure`, `ceph_configure`, and `clustering.sh` preseeds all set `volume.size="${DEFAULT_VOLUME_SIZE}"` (`24MiB`), matching `SMALL_ROOT_DISK="root,size=${DEFAULT_VOLUME_SIZE}"`. That alignment makes `-d "${SMALL_ROOT_DISK}"` effectively free on containers (`2.73s` vs. `2.76s` for `lxc init testimage` on Ceph; `690ms` vs. `690ms` for `lxc launch testimage` on LVM thin) because LXD skips block-device/ext4 resizing when the requested size matches the cloned image volume—while still preventing 10 GiB disks on empty VMs and secondary pools.
2. **CoW image cache on `btrfs`/`zfs` vs. tarball unpack on `dir`:** On `btrfs`, the first `lxc init testimage` unpacks the tarball into the pool's image subvolume (`~110ms`), after which every subsequent `lxc init testimage` is a `~42–51ms` CoW clone—roughly **2× faster than `dir`** (`91ms`, which unpacks every time).
3. **Graceful `lxc stop` is ~10× slower than `lxc stop -f`:** Busybox's `init` does not cleanly power off on `SIGPWR`, so `lxc stop` waits ~2 seconds before completing (`2.21s–2.82s` vs. `158ms–789ms` for `lxc stop -f`). Always pass `-f` unless the test specifically verifies graceful shutdown.
4. **Non-thin LVM snapshot restore (`11.0s`):** Non-thin LVM uses `lvconvert --merge` and waits for merge + reactivation (`38×` slower than thin LVM and `289×` slower than `btrfs`).
5. **Block vs. filesystem custom volumes on Ceph:** Block volumes skip `rbd map` + `mkfs.ext4` + `rbd unmap`, making them **2.3× faster** (`704ms` vs. `1.62s`) when a test only needs a volume object rather than a mounted filesystem. (Conversely, on ZFS, zvol creation incurs udev device-node settle time, so block volumes can be slower than dataset filesystem volumes.)

### `LXD_TMPFS=1` vs. Disk-Backed (`LXD_TMPFS=0`)

Running `test/main.sh` with `LXD_TMPFS=1` (as CI does) yields the largest speedups on metadata/sync-heavy copy-on-write backends:

| Operation | `btrfs` (disk / tmpfs) | `dir` (disk / tmpfs) | `lvm` (disk / tmpfs) | `zfs` (disk / tmpfs) |
| :--- | ---: | ---: | ---: | ---: |
| `lxc storage create` (secondary) | 85 / 42ms (**2.0×**) | 33 / 28ms (1.2×) | 408 / 374ms (1.1×) | 412 / 225ms (**1.8×**) |
| `lxc init --empty` | 50 / 38ms (1.3×) | 45 / 36ms (1.2×) | 495 / 347ms (1.4×) | 159 / 69ms (**2.3×**) |
| `lxc init testimage` | 61 / 48ms (1.3×) | 133 / 111ms (1.2×) | 482 / 490ms (1.0×) | 219 / 100ms (**2.2×**) |
| `lxc snapshot` | 53 / 43ms (1.2×) | 150 / 120ms (1.2×) | 242 / 234ms (1.0×) | 85 / 50ms (**1.7×**) |
| `lxc copy` (same pool) | 83 / 69ms (1.2×) | 332 / 314ms (1.1×) | 627 / 534ms (1.2×) | 314 / 163ms (**1.9×**) |
| `lxc import` | 199 / 182ms (1.1×) | 198 / 184ms (1.1×) | 988 / 739ms (1.3×) | 347 / 209ms (**1.7×**) |
| `lxc storage volume create` (fs) | 33 / 23ms (1.4×) | 27 / 21ms (1.3×) | 255 / 219ms (1.2×) | 95 / 54ms (**1.8×**) |

---

## 3. `system-tests` (`test/main.sh`): Storage-Agnostic Operations & Cluster Overhead

Measured on `dir` + `tmpfs`, comparing a **standalone** daemon against a **3-node cluster** (`spawn_lxd_and_bootstrap_cluster` + 2 joins, with the 10-instance benchmark distributing running containers across `node1`–`node3`).

| Operation | Standalone | 3-Node Cluster | Cluster / Standalone | Notes |
| :--- | ---: | ---: | ---: | :--- |
| **Topology Setup / Teardown** | | | | |
| `spawn_lxd` (1 node) vs. 3-node bootstrap + 2 joins | 1.14s | 5.56s | **4.9×** | Each extra member adds `~2.2s` |
| `kill_lxd` (per daemon) | 514ms | — | — | Faster than `shutdown_lxd` when dirty state is acceptable |
| **Read Operations (0 / 1 / 10 running instances)** | | | | |
| `lxc query /1.0` | 10ms | 23ms | 2.3× | |
| `lxc query /1.0/instances` (0 / 1 / 10 inst) | 8 / 9 / 12ms | 8 / 14 / 11ms | **1.0×** | O(1) DB-only list of URLs |
| `lxc query /1.0/instances?recursion=1` (1 / 10 inst) | 16 / 17ms | 17 / 33ms | 1.1–1.9× | DB-only config/metadata |
| `lxc query /1.0/instances?recursion=2` (1 / 10 inst) | 23 / 62ms | 47 / **172ms** | **2.0–2.8×** | Fans out to every node for live state |
| `lxc list -f csv -c n` (0 / 1 / 10 inst) | 10 / 15 / 17ms | 9 / 13 / 32ms | 0.9–1.9× | Avoids `recursion=2` state fetch |
| `lxc list -f csv -c ns` (1 / 10 inst) | 20 / 18ms | 13 / 27ms | 0.7–1.5× | Cheapest way to check name + status |
| `lxc list` (default table, 0 / 1 / 10 inst) | 11 / 17 / 29ms | 12 / 37 / **176ms** | **2.2–6.1×** | Fetches IPs, state, snapshots (`recursion=2`) |
| `lxc list -f json` (1 / 10 inst) | 23 / 31ms | 41 / **166ms** | **1.8–5.4×** | Full `recursion=2` payload |
| `lxc info <inst>` (running) | 23ms | 47ms | **2.0×** | Includes live state + snapshots + backups |
| `lxc config show <inst>` | 13ms | 16ms | 1.2× | DB config only |
| `lxc query /1.0/instances/<inst>` | 11ms | 13ms | 1.2× | DB config only |
| `lxc query /1.0/instances/<inst>/state` | 15ms | 52ms | **3.5×** | Live runtime state query |
| `lxc cluster list` | — | 15ms | — | |
| **Writes, Exec & Forwarding** | | | | |
| `lxc exec -- true` | 36ms | 38ms | 1.1× | Local container |
| `lxc config set <inst> user.bench=1` | 35ms | 42ms | 1.2× | Single dqlite write |
| `lxc profile create + delete` | 24ms | 26ms | 1.1× | |
| `lxc project create + delete` | 34ms | **159ms** | **4.7×** | Multi-table Raft transaction + profile/default setup |
| `lxc network create + delete` (bridge) | 148ms | — | — | Kernel bridge + dnsmasq setup/teardown |
| `lxc init --empty -d SMALL_ROOT_DISK` (local `node1`) | 31ms | 63ms | **2.0×** | |
| `lxc init --empty -d SMALL_ROOT_DISK` (forwarded `node2`) | — | 75ms | **2.4×** | Adds `+12ms` internal TLS hop |
| `lxc launch testimage -d SMALL_ROOT_DISK` (local `node1`) | 315ms | 262ms | ~1.0× | Dominated by container start |
| `lxc launch testimage -d SMALL_ROOT_DISK` (forwarded `node2`) | — | 309ms | ~1.2× | Adds `+47ms` over local cluster launch |

### Key Takeaways for Storage-Agnostic & Cluster Operations

- **`lxc list` scales poorly in clusters:** With 10 running instances in a 3-node cluster, bare `lxc list` (`176ms`) is **6.5× slower** than `lxc list -f csv -c ns` (`27ms`) and **16× slower** than `lxc query /1.0/instances` (`11ms`), because the default columns require `recursion=2` live state fan-out across every member. Inside polling loops (`for _ in $(seq 30)`), that difference adds seconds per loop.
- **Prefer targeted endpoints over `lxc info`:** If you only need a config key or instance existence, `lxc config show` / `lxc query /1.0/instances/<inst>` (`13ms`) avoids fetching live state, snapshots, and backups (`47ms` in a cluster).
- **Cluster setup dominates short tests:** Bootstrapping a 3-node cluster takes `~5.6s` (and a 5-node cluster `~10s+`). Group related cluster assertions into fewer cluster lifecycles rather than spawning a fresh 3-node cluster for trivial checks.

---

## 4. `snap-tests` (`test/snap/`): Snap-Specific Operations

Snap tests exercise the full LXD snap against real OS images (`ubuntu-minimal-daily:24.04`), full systemd boot, KVM virtual machines, and nested snapd/LXD. Their baselines are **1–2 orders of magnitude higher** than `test/main.sh` busybox operations.

Measured with `./test/benchmark-ops.sh --mode snap --nested` on `lxd-snap-test` (local cached `ubuntu-minimal-daily:24.04` container `132 MiB` and VM `248 MiB` images, disk-backed pools).

### 4.1 Real Ubuntu Minimal Container Operations

| Operation | `btrfs` | `ceph` | `dir` | `lvm` (non-thin) | `lvm-thin` | `zfs` | Slowest / Fastest |
| :--- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `lxc storage create` (snap pool) | 211ms | 4.90s | 116ms | 342ms | 621ms | 460ms | **42×** |
| `lxc init <ubuntu-ct>` (cold pool cache) | 1.75s | 7.92s | 1.36s | 2.33s | 4.18s | 1.94s | **5.8×** |
| `lxc launch <ubuntu-ct>` (warm pool cache)* | **408ms** | 3.62s | 1.99s | 2.65s | 1.19s | **496ms** | **8.9×** |
| `waitInstanceReady` (after launch) | 166ms | 135ms | 176ms | 1.28s | 168ms | 1.23s | ~1 poll tick |
| `waitInstanceBooted` (extra after ready) | **4.65s** | **3.21s** | **3.78s** | **1.92s** | **6.11s** | **2.42s** | +2–6s per ct |
| `lxc exec -- true` | 150ms | 109ms | 133ms | 133ms | 170ms | 120ms | 1.6× |
| `lxc snapshot` | **189ms** | 1.20s | 1.72s | 290ms | 363ms | **135ms** | **13×** |
| `lxc copy` (stopped + 1 snapshot) | **187ms** | 5.05s | 3.17s | 5.19s | 780ms | **413ms** | **28×** |
| `lxc stop -f` | 867ms | 1.50s | 918ms | 1.01s | 989ms | 853ms | 1.8× |
| `lxc delete -f` | **180ms** | 1.98s | 525ms | 351ms | 405ms | **204ms** | **11×** |

*\* On CoW drivers (`btrfs`, `ceph`, `lvm-thin`, `zfs`), the first `init` unpacks the 132 MiB image into a cached pool volume; subsequent `launch` calls clone it in `400–500ms` on `btrfs`/`zfs` (vs. `2.0–2.7s` on `dir`/`lvm` non-thin, which unpack every time).*

### 4.2 Real Ubuntu Minimal VM Operations (`3584MiB` Root Disk)

| Operation | `btrfs` | `ceph` | `dir` | `lvm` (non-thin) | `lvm-thin` | `zfs` | Slowest / Fastest |
| :--- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `lxc init <ubuntu-vm> --vm` (cold cache) | 5.63s | **26.6s** | 2.77s | 3.44s | 6.90s | 6.06s | **9.6×** |
| `lxc start <ubuntu-vm>` (QEMU spawn) | 313ms | 574ms | 341ms | 416ms | 616ms | 359ms | 2.0× |
| `waitInstanceReady` (VM agent up) | **27.8s** | **27.0s** | **28.2s** | **27.8s** | **25.7s** | **25.0s** | ~25–28s fixed |
| `waitInstanceBooted` (extra after ready) | **+8.14s** | **+8.93s** | **+10.9s** | **+6.63s** | **+7.45s** | **+8.52s** | +7–11s fixed |
| `lxc stop -f <ubuntu-vm>` | 521ms | 1.63s | 522ms | 562ms | 569ms | 486ms | 3.4× |
| `lxc snapshot` (VM) | **152ms** | 4.91s | 1.63s | 907ms | 700ms | **242ms** | **32×** |
| `lxc restore` (VM) | **212ms** | 3.94s | 2.03s | **38.9s** | 774ms | **263ms** | **183×** |
| `lxc copy` (VM + 1 snapshot) | **195ms** | 8.89s | 5.29s | **21.8s** | 1.38s | **1.01s** | **112×** |
| `lxc delete -f <ubuntu-vm>` | **165ms** | 4.11s | 171ms | 526ms | 492ms | 265ms | **25×** |

### 4.3 Nested Snapd / LXD & Snap-Test Infrastructure Overhead

| Operation / Phase | Measured Cost | Source |
| :--- | ---: | :--- |
| `snap wait system seed.loaded` (inside booted container) | ~143ms | Local `lxd-snap-test` |
| `snap install lxd` (inside nested container, from store) | **~38.3s** | Local `lxd-snap-test` |
| Snap-test runner setup (no disk reclaim, no MicroCeph: `cgroup`, `cluster`, `vm`) | **40–67s** | GitHub Actions `tests.yml` |
| Snap-test runner setup (with `reclaim-disk-space`: `storage-vm`, `nesting`) | **96–169s** | GitHub Actions `tests.yml` |
| Snap-test runner setup (with MicroCeph: `storage-vm ceph`) | **~442s (7.4 min)** | GitHub Actions `tests.yml` |
| `system-tests` `Setup MicroCeph` step (3 OSDs) | **60–93s** | GitHub Actions `tests.yml` |
| `system-tests` `Setup MicroOVN` step | **7–32s** | GitHub Actions `tests.yml` |
| `test/snap/cluster`: deploy 3-node nested snap cluster | **~108s** | GitHub Actions `tests.yml` |
| `test/snap/cluster`: upgrade 3-node cluster to `latest/edge` | **~117s** | GitHub Actions `tests.yml` |

### Key Takeaways for `snap-tests`

- **Every real VM boot costs ~30–40s:** `waitInstanceReady` alone takes `25–28s` (UEFI + kernel + `lxd-agent` start), and `waitInstanceBooted` adds another `7–11s`. Reuse a single running VM across sub-tests whenever state isolation allows.
- **`waitInstanceReady` vs. `waitInstanceBooted`:** For containers, `waitInstanceReady` finishes in `~0.2–1.3s`, whereas `waitInstanceBooted` waits for full `systemctl is-system-running --wait` (`+2–6s`, plus a hardcoded `sleep 1`). Only call `waitInstanceBooted` (or `isSystemdClean`) when systemd units or cloud-init completion actually matter.
- **Prefer `zfs` for VM-heavy tests (avoid `btrfs`, `dir`, and non-thin `lvm`):** Although `btrfs` posts the lowest single-run times here, it is not recommended for VM storage because of CoW issues with `.raw` disk files. `zfs` is better suited thanks to ZVol block devices and compression, while still delivering fast CoW operations: on a 3.5 GiB VM, `zfs` copies a VM with a snapshot in `1.01s` vs. `5.29s` on `dir` (**5.2×**) and `21.8s` on non-thin `lvm` (**22×**), and restores a VM snapshot in `263ms` vs. `2.03s` on `dir` (**7.7×**) and `38.9s` on non-thin `lvm` (**148×**).

---

## 5. Real GitHub Actions CI Timings (`tests.yml`)

Data from green `main` runs with `fast-backend=dir` (scheduled) and `fast-backend=btrfs` (push).

### 5.1 `system-tests` Matrix Wall-Clock Times

| Matrix Job | `fast-backend=dir` (test step / total) | `fast-backend=btrfs` (total) | Notes |
| :--- | ---: | ---: | :--- |
| **`System (standalone_storage, ceph)`** | **86.6m / 88.9m** | **83.1m** | **Pipeline critical path** |
| `System (standalone_storage, zfs)` | 38.5m / 39.5m | 35.4m | `storage_block_tracking_vm` + `migration` = 66% |
| `System (standalone_storage, lvm)` | 33.0m / 34.2m | 26.0m | `storage_block_tracking_vm` + `migration` = 51% |
| `System (replicator_storage, all)` | 31.8m / 33.9m | 32.3m | `ceph` alone is 927s (48% of 6-backend loop) |
| `System (standalone_storage, fasts)` | 29.6m / 31.3m | 26.7m | `btrfs` 20.2m (`block_tracking_vm` 10m) + `dir` 9.4m |
| `System (cluster, fast)` | 21.7m / 22.7m (`dir`) | 23.4m (`btrfs`) | 50 cluster tests; `dir` and `btrfs` nearly identical |
| `System (cluster_storage, all)` | 20.4m / 22.5m | 23.6m | `ceph` 393s vs. `dir` 114s (`3.4×`) |
| `System (standalone_storage, random)` | 14.8m / 15.7m | 29.0m | Variance depends on picked backend |
| `System (snap, all)` | 11.4m / 13.7m | 17.8m | 4 snap-host tests across 6 backends |
| `System (standalone, fast)` | 12.7m / 13.6m (`dir`) | 12.2m (`btrfs`) | 75 standalone tests |
| `System (network, fast)` | 12.5m / 13.6m (`dir`) | 14.8m (`btrfs`) | `network_ovn` alone is 508s (69% of group) |
| `System (instance, fasts)` | 10.1m / 11.0m | 9.7m | `btrfs` 309s + `dir` 299s |
| `System (image, fasts)` | 3.0m / 4.0m | 3.6m | `btrfs` 95s + `dir` 83s |
| `System (cluster_cephfs, ceph)` | 0.8m / 2.6m | 8.1m | Setup MicroCeph (67–93s) dominates |

### 5.2 Slowest Individual `system-tests` Functions

| Test Function | Group | `btrfs` | `ceph` | `dir` | `lvm` | `zfs` | Ceph / `dir` |
| :--- | :--- | ---: | ---: | ---: | ---: | ---: | ---: |
| `storage_block_tracking_vm` | `standalone_storage` | 603.6s | **1495.1s** | — | 717.9s | 769.1s | — |
| `migration` | `standalone_storage` | 54.9s | 600.0s | 67.5s | 280.0s | **741.3s** | 8.9× |
| `network_ovn` | `network` | — | — | **507.8s** | — | — | — |
| `storage_local_volume_handling` | `standalone_storage` | 20.6s | **286.4s** | 21.9s | 33.8s | 40.7s | **13.1×** |
| `projects_restrictions` | `standalone_storage` | 40.9s | **268.2s** | 42.0s | 77.0s | 49.7s | **6.4×** |
| `shutdown` | `standalone` | — | — | **232.8s** | — | — | — |
| `clustering_replicator_vm` | `replicator_storage` | 43.1s | **199.9s** | — | — | 49.3s | — |
| `container_copy_start` | `standalone_storage` | 27.6s | **180.5s** | 31.4s | 51.4s | 30.5s | 5.7× |
| `clustering_roles` | `cluster` | — | — | **167.5s** | — | — | — |
| `storage_volume_snapshots` | `standalone_storage` | 16.3s | **160.2s** | 18.5s | 39.0s | 20.8s | 8.7× |
| `clustering_durable_operations` | `cluster` | — | — | **150.6s** | — | — | — |
| `backup_import` | `standalone_storage` | 24.7s | **146.8s** | 16.3s | 35.6s | 27.4s | 9.0× |
| `clustering_replicator_volumes` | `replicator_storage` | 12.5s | **146.0s** | 18.1s | 35.2s | 15.8s | 8.1× |
| `clustering_waitready` | `cluster` | — | — | **142.5s** | — | — | — |
| `backup_volume_export` | `standalone_storage` | 14.7s | **140.6s** | 9.8s | 21.4s | 17.0s | **14.3×** |
| `authorization` | `standalone` | — | — | **121.1s** | — | — | — |

### 5.3 `snap-tests` Wall-Clock Times (Top Jobs & `storage-vm` Matrix)

| Snap Job | Total Wall-Clock | Setup Overhead | Test Execution | Notes |
| :--- | ---: | ---: | ---: | :--- |
| `Snap (26.04, csi)` | **40.5m** | ~1m | ~39.5m | Kubernetes / CSI driver setup & tests |
| `Snap (26.04, storage-volumes-vm)` | **40.1m** | ~2m | ~38.1m | Multiple real VM boots + volume ops |
| `Snap (26.04, storage-vm ceph)` | **28.9m** | 7.4m | 21.5m | MicroCeph setup dominates setup phase |
| `Snap (26.04, ui firefox)` / `chromium` | **28.1m** / **25.3m** | ~2m | ~23–26m | Full browser E2E suite |
| `Snap (26.04, storage-metadata)` | **25.7m** | ~2m | ~23.7m | |
| `Snap (26.04, conversion)` | **25.2m** | ~2m | ~23.2m | |
| `Snap (26.04, storage-vm lvm-thin)` | **24.6m** | 2.1m | 22.5m | Remote refresh + same-pool migration = 390s |
| `Snap (26.04, storage-vm lvm)` | **23.6m** | 2.8m | 20.8m | Non-thin snapshot restore/copy (`62s`/`78s`) |
| `Snap (26.04, storage-vm dir)` | **21.6m** | 2.5m | 19.1m | No CoW: copy/migrate/refresh `65–76s` each |
| `Snap (26.04, network-ovn ovn:deb)` / `edge` | **18.6m** / **17.7m** | ~2m | ~16m | 29× `waitInstanceBooted`, 13× bare `lxc list` |
| `Snap (26.04, storage-vm zfs)` | **17.0m** | 1.6m | 15.4m | CoW snapshots (`19s` restore, `24s` copy) |
| `Snap (26.04, storage-vm btrfs)` | **14.9m** | 2.3m | 12.6m | Lowest CI runtime (`16s` restore, `17s` copy), though `zfs` is preferred for VM storage |
| `Snap (26.04, nesting)` | **14.6m** | 2.6m | 12.0m | Nested containers + nested VMs |
| `Snap (26.04, lxd-installer)` | **14.6m** | ~2m | ~12.6m | |
| Fastest snap suites (`bgp`, `docker`, `cgroup`, `interception`, `lxd-user`, `cpu-vm`) | **1.7–1.9m** | 0.7–1.1m | 0.8–1.0m | Setup accounts for >50% of job time |

---

## 6. Optimization Opportunities Spotted in Existing `test/`

An inventory of `test/suites/*.sh` and `test/snap/*` combined with the CI timing tables above highlights several high-impact optimization candidates:

1. **Critical path: `test_storage_block_tracking_vm` (`suites/storage_block_tracking.sh`)**
   - Takes **24.9 min on `ceph`** (29% of the entire `standalone_storage, ceph` critical-path job), **12.8 min on `zfs`**, **12.0 min on `lvm`**, and **10.1 min on `btrfs`**.
   - Splitting `storage_block_tracking_vm` into its own parallel matrix group (or trimming redundant VM reboot/full-disk NBD rewrite iterations on slower drivers) would immediately cut ~25 minutes off total CI wall-clock time.

2. **Storage-agnostic restriction checks running across all 5 backends (`test_projects_restrictions` in `suites/projects.sh`)**
   - Runs the entire `run_projects_restrictions` function **4 times** (Unix socket, unrestricted TLS, restricted TLS, fine-grained TLS) on **every backend** (`standalone_storage`), costing **268s on Ceph** (`6.4×` vs. `dir`), `77s` on LVM, `50s` on ZFS, `41s` on btrfs, `42s` on dir (~8 min cumulative runner time).
   - Most of `run_projects_restrictions` validates network/profile/config authorization rules that are storage-driver agnostic; splitting the storage-volume restriction subset from the API/auth restriction checks (moving the latter to `standalone`) would save ~3.5+ minutes on the Ceph critical path.

3. **Unneeded `lxc init testimage` and `lxc launch testimage` in multi-backend storage tests**
   - Across `test/suites/*.sh`, `lxc init testimage` appears **141 times** without `SMALL_ROOT_DISK` and `lxc launch testimage` **150 times**. Several multi-backend tests unpack or boot `testimage` when `--empty` (or `init` instead of `launch`) would test the exact same code path:
     - `suites/container_move.sh` (**71.8s on Ceph**, `19.5×` vs. `dir`): creates 8 containers (`c1`–`c8`) with `lxc init testimage` and never starts or execs any of them—switching to `lxc init --empty` (and setting `volume.size="${DEFAULT_VOLUME_SIZE}"` on `pool2`) avoids 8 image-clone + 10 GiB pool-volume operations per backend.
     - `suites/storage_profiles.sh` (**39.4s on Ceph**, `6.3×` vs. `dir`): launches 3 running `testimage` containers (`c1`–`c3`) to verify profile root-device restrictions without ever calling `lxc exec`; `lxc init --empty` would be **~2×–9× faster**.
     - `suites/storage.sh` (**87.8s on Ceph**, `17.5×` vs. `dir`): creates numerous secondary pools and 27 `init`/`launch` calls without `SMALL_ROOT_DISK` or `volume.size="${DEFAULT_VOLUME_SIZE}"`.

4. **Secondary pools without `volume.size="${DEFAULT_VOLUME_SIZE}"`**
   - Tests that create secondary `lvm`/`ceph` pools (e.g., `suites/container_move.sh`, `suites/storage_profiles.sh`, `suites/migration.sh`) without `volume.size="${DEFAULT_VOLUME_SIZE}"` inherit LXD's 10 GiB default volume size (`~4.4×` slower per volume on LVM). Passing `volume.size="${DEFAULT_VOLUME_SIZE}"` at pool creation time keeps them aligned with `SMALL_ROOT_DISK` (`24MiB`) and avoids both 10 GiB formatting and resize overhead.

5. **Overuse of `waitInstanceBooted` and bare `lxc list` in `test/snap/`**
   - `test/snap/network-ovn` (**18.6 min** in CI) calls `waitInstanceBooted` **29 times** (and `waitInstanceReady` 0 times), plus bare `lxc list` **13 times**. Switching instances that only need networking/exec readiness to `waitInstanceReady` (as `test/snap/storage-vm` already does 21 times) can save `~1–2 min` per OVN matrix job.
   - `test/snap/network` similarly calls `waitInstanceBooted` 9 times and bare `lxc list` 4 times.

6. **Fixed `sleep` waits in cluster tests**
   - `test_clustering_roles` (`suites/clustering.sh`, **167.5s** on `dir`—the slowest test in `group:cluster`) executes repeated `sleep 12` statements (totaling `144s` of pure wall-clock sleeping) waiting for `cluster.offline_threshold=11`. Replacing fixed sleeps with bounded polling for role convergence (or a shorter `offline_threshold` where protocol semantics allow) would reclaim most of its runtime.

7. **Bare `lxc list` and graceful `lxc stop` in `test/suites/`**
   - Bare `lxc list` (without `-f csv -c ...`) still appears **31 times** in `test/suites/*.sh` (notably 22 in `basic.sh` and 6 in `clustering_evacuation`) and **23 times** in `test/snap/*`.
   - Graceful `lxc stop` (without `-f`, costing `~2.2s` vs. `~170ms`) appears 8 times in `test/suites/*.sh` and 6 times in `test/snap/*`; keep it only where graceful shutdown behavior is the thing being tested.

8. **Split multi-call `lxc` sequences across `test/suites/` and `test/snap/`**
   - Scanning both suites shows frequent back-to-back calls that can be collapsed where intermediate states are not under test: **169** consecutive `set` calls on the same resource (e.g., `lxc config/network/profile/project set` one key at a time), **69** `lxc init` + `lxc config set`/`device add` pairs, **56** consecutive single-instance `lxc delete` calls, **17** `project`/`network`/`storage create` + `set` pairs, plus `init` + `start` (15), `stop` + `start` (11), and `stop` + `delete` (8) pairs. Combining these follows the patterns in Section 1 and cuts redundant CLI/API round-trips (especially on Ceph and clustered tests).

---

## 7. Reproducing and Refreshing Measurements

Use [`./benchmark-ops.sh`](./benchmark-ops.sh) (run as root from inside `test/` or anywhere in the checkout). On Ubuntu 26.04+, `sudo` does not support `-E` and strips `LD_LIBRARY_PATH`, so preserve variables explicitly and pass `LD_LIBRARY_PATH` through `env`:

```sh
# Evaluate local dqlite/liblxc library paths when running system mode against a source build
eval "$(make env)"
SUDO_PRESERVE_ENV="PATH,GOPATH,LXD_CEPH_CLUSTER,LXD_TEST_IMAGE,LXD_VM_TESTS,SMALL_ROOT_DISK,TMPDIR"

# 1. Storage-dependent ops across available backends (tmpfs) + standalone vs 3-node cluster
sudo --preserve-env="${SUDO_PRESERVE_ENV}" env "LD_LIBRARY_PATH=${LD_LIBRARY_PATH:-}" \
  ./test/benchmark-ops.sh --mode system --tmpfs --cluster --output /tmp/bench-system.tsv

# 2. Compare tmpfs vs disk-backed performance
sudo --preserve-env="${SUDO_PRESERVE_ENV}" env "LD_LIBRARY_PATH=${LD_LIBRARY_PATH:-}" \
  ./test/benchmark-ops.sh --mode system --backends "btrfs dir lvm zfs" --compare-tmpfs --no-cluster

# 3. Snap-tests operations (uses the installed LXD snap; real Ubuntu minimal containers & VMs + nested snapd/LXD)
sudo ./test/benchmark-ops.sh --mode snap --snap-backends "btrfs ceph dir lvm lvm-thin zfs" --nested --output /tmp/bench-snap.tsv
```

When comparing across runs or hardware, normalize timings against `lxc init --empty` on `dir` (for `system-tests`) or `lxc init <ubuntu-ct>` on `btrfs` (for `snap-tests`), and cross-check backend multipliers against the `generate_duration_table` summaries printed at the end of `.github/workflows/tests.yml` jobs.
