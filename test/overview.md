# LXD testing

The LXD test suite runs from this repository. It includes code and functional
integration tests, snap integration tests in `test/snap/`, and GPU passthrough
tests dispatched to Testflinger.

## CI workflows

Testing happens on GitHub-hosted runners that are `amd64` based runners.

```mermaid
graph TD
    subgraph LXD_Core [CI Flow: canonical/lxd]
        direction TB
        START([Trigger Event]) --> CT[code-tests: Build & Unit]

        %% code-tests detail
        CT --> CT1[golangci-lint]
        CT --> CT2[make check-unit]
        CT --> CT3[Build Binaries]

        %% Downstream Jobs
        CT3 --> ST[system-tests]
        CT3 --> SNP[snap-tests]
        CT3 --> CLI[client]
        CT3 --> DOC[documentation]

        %% Doc
        DOC --> DOCLINK[documentation links]

        %% UI
        CT3 & DOC --> UI[ui-e2e-tests]

        %% TICS
        ST & SNP & CLI & UI --> TICS[TICS: Code Quality & Coverage]

        %% Final Release Action
        TICS --> SNPRELEASE[Trigger Launchpad Snap Build]
    end

    subgraph Triggers [Frequency & Triggers]
        T1[Push/PR: Code, Snap & System Tests]
        T2[Daily: Full Matrix Integration & Coverage]
        T3[Manual: Parametrized Tests]
    end
```

### Code tests

The **`code-tests`** job acts as the primary build and verification stage, providing the binaries and environment state required for all downstream integration tests.

* **Static Verification**: Performs immediate code quality checks via `ShellCheck` and `golangci-lint` to catch issues before resource-heavy tests begin.
* **Dependency Management**: Consolidates the installation of system-level build dependencies and complex C-based libraries like `dqlite` and `liblxc`.
* **Artifact Preparation**: Builds a distribution tarball and compiles the full set of LXD binaries (e.g., `lxc`, `lxd`, `lxd-agent`, `fuidshift`), which are then uploaded for use by the `system-tests` and `snap-tests` jobs.
* **Cache Priming**: Pre-downloads large external assets like test images and snap dependencies to reduce execution time in subsequent matrix jobs.
* **Integration Logic**: Randomly determines a "fast backend" (`btrfs` or `dir`) to pass to downstream jobs, ensuring integration tests run against at least one efficient storage driver during PR validation.

```mermaid
graph TD
    subgraph CT_Cache [Cached assets]
        direction LR
        O1[deps - liblxc/dqlite]
        O2[bins - lxd/lxc/dqlite]
        O3[snaps - lxd/microceph/microovn]
        O4[images - ubuntu-minimal-daily VM & container]
    end
    subgraph CT [Code tests]
        direction TB
        Start(["**code-tests**"]) --> ShellCheck[Differential ShellCheck]
        ShellCheck --> InstallDeps[Install build dependencies, Go modules & dqlite/liblxc]
        InstallDeps --> BuildProcess[Create LXD tarball & build binaries]
        BuildProcess --> StaticAnalysis[Run golangci-lint & generic static analysis]
        StaticAnalysis --> UnitTests[Execute unit tests]
        UnitTests --> PrimeCaches[Prime caches: External images & snaps]
        PrimeCaches --> UploadArtifacts[Upload built binaries as system-test-deps]
        UploadArtifacts --> PickBackend[Logic: Select fast backend for system tests]
    end
```

### Client tests

The **`client`** job validates the LXD command-line tools across different operating systems to ensure cross-platform compatibility.

* **Cross-Platform Matrix**: Executes on Ubuntu, macOS, and Windows runners to verify the LXD client environment globally.
* **Static Compilation**: Builds static versions of the `lxc` tool for both `arm64` and `amd64` architectures on all target platforms.
* **Tool Building**: Additional tools like `lxd-benchmark` and `lxd-convert` are built specifically on Linux runners.
* **Test Suites**: Runs three distinct sets of unit tests covering the client library, the `lxc` command-line logic, and shared utility code.
* **Artifact Preservation**: If the workflow is triggered by a `push` event, the built binaries are uploaded as job artifacts for distribution or further testing.

```mermaid
graph TD
    subgraph CT_Matrix [Matrix strategy]
        direction LR
        O_Node{OS}

        O_Node --- O1[ubuntu]
        O_Node --- O2[macos]
        O_Node --- O3[windows]
    end
    subgraph CT [Client tests]
        direction TB
        Start(["**Dimension**: **OS** - ubuntu, macos, windows"]) --> BuildLXC[Build static lxc: aarch64 & x86_64]

        BuildLXC --> LinuxCheck{Is runner OS Linux?}
        LinuxCheck -- Yes --> BuildTools[Build static lxd-benchmark & lxd-convert]
        LinuxCheck -- No --> UnitTests
        BuildTools --> UnitTests[Execute unit tests: client, lxc, & shared suites]

    end
```

### Snap tests

The **`snap-tests`** job validates LXD's behavior as a snap package across the
configured Ubuntu releases.

* **Test execution**: Runs the executable scripts in `test/snap/` through `test/snap.sh`.
* **LXD binary integration**: Sideloads the binaries built during the current run into the selected LXD snap, ensuring the tests exercise the proposed code.
* **Infrastructure preparation**: Dynamically configures **MicroCeph** and **MicroOVN** to provide clustered storage and networking services needed for the snap-based integration tests.
* **Matrix Dimensions**:
    * **OS**: Validates compatibility across the configured Ubuntu releases.
    * **Test suites**: Runs the snap test scripts, including `cloud-init`, `network-ovn`, `vm-migration`, UI tests, and various `storage-vm` backends.
* **Resource Management**: Includes aggressive memory and disk space reclamation steps to ensure the GitHub runner has enough headroom for intensive virtual machine tests.

```mermaid
graph TD
    subgraph ST_Matrix [Matrix strategy]
        direction LR
        O_Node{OS}
        S_Node{Suite}

        O_Node --- O1[configured Ubuntu release]

        S_Node --- S1[cgroup]
        S_Node --- S2[cloud-init]
        S_Node --- S3[cluster]
        S_Node --- S4[container]
        S_Node --- S5_36[...]
        S_Node --- S37[ui chromium]
        S_Node --- S38[vm]
        S_Node --- S39[vm-nesting]
        S_Node --- S40[vm-migration]
    end
    subgraph ST [Snap tests]
        direction TB
        Start(["**Dimensions**: **OS** x **test suite**"]) --> PullArtifacts[Download dependencies: dqlite, images, snaps, & LXD binaries]

        PullArtifacts --> InfraCheck{Setup MicroCeph?}
        InfraCheck -- Yes --> SetupCeph[Setup MicroCeph 3-node cluster]
        InfraCheck -- No --> SetupOVN[Setup MicroOVN]
        SetupCeph --> SetupOVN

        SetupOVN --> ExecTest[Execute test/snap.sh test/snap/matrix.test]

        ExecTest --> Uploads[Upload reports, crash dumps, & coverage data]
    end
```

### System tests

The **`system-tests`** job is an integration suite that executes functional test groups against various storage backends.

* **Matrix Dimensions**: The job runs across eight functional groups (`cluster`, `cluster_storage`, `image`, `instance`, `network`, `snap`, `standalone`, `standalone_storage`) and six storage backends (`btrfs`, `ceph`, `dir`, `lvm`, `zfs`, `random`).
* **Sequential vs. Parallel**:
  * For the `standalone_storage` group, backends are tested in **parallel** (one backend per runner).
  * For the `cluster_storage` and `snap` groups, all backends are tested **sequentially** on a single runner.

* **Infrastructure**: The suite dynamically deploys **MicroCeph** (if required) and **MicroOVN** on the runner to provide the necessary clustered storage and networking environment.
* **Binaries**: This job does not compile LXD; it downloads the binaries built in the previous `code-tests` stage.

```mermaid
graph TD
    subgraph ST_Matrix [Matrix strategy]
        direction LR
        G_Node{Group}
        B_Node{Backend}

        G_Node --- G1[cluster]
        G_Node --- G2[cluster_storage]
        G_Node --- G3[network]
        G_Node --- G4[image]
        G_Node --- G5[instance]
        G_Node --- G6[snap]
        G_Node --- G7[standalone]
        G_Node --- G8[standalone_storage]

        B_Node --- B1[fast: btrfs or dir]
        B_Node --- B2[all: Sequential Loop]
        B_Node --- B3[Explicit: zfs, ceph, lvm, etc.]
    end
    subgraph ST [System tests]
        direction TB
        Matrix(["**Dimensions**: **group** x **backends**"]) --> InstallDeps[Install runtime & dqlite/liblxc dependencies]
        InstallDeps --> PullArtifacts[Download images, snaps & binaries]

        PullArtifacts --> CephCheck{Is backend ceph or all?}
        CephCheck -- Yes --> SetupCeph[Setup MicroCeph 3-node cluster]
        CephCheck -- No --> SetupOVN[Setup MicroOVN]
        SetupCeph --> SetupOVN

        SetupOVN --> EnvSetup[System Environment Setup]
        EnvSetup --> ExecTest[Execute main.sh group:GROUP for assigned backend]

        ExecTest --> Uploads[Upload crash dumps & coverage data]
    end
```

## GPU passthrough tests

This workflow is a pipeline that automates the validation of NVIDIA GPU passthrough for containers using specialized hardware.

* **Specialized Infrastructure**: These tests execute on **self-hosted** runners due to needing to launch jobs via [Testflinger](https://canonical-testflinger.readthedocs-hosted.com/latest/) that is not accessible from GitHub-hosted runners.
* **Testflinger Integration**: The workflow does not run the tests directly on the **self-hosted** runner; instead, it uses the `canonical/testflinger` action to submit jobs to a dedicated hardware queue ([`lxd-nvidia`](https://testflinger.canonical.com/queues/lxd-nvidia)) with a set of physical machine with the needed hardware available.
* **Test Variants**:
  * **CDI Tests**: Validates the modern **Container Device Interface** (CDI) passthrough on both Ubuntu Core 24 and standard Ubuntu releases.
  * **Legacy Runtime**: Validates the older NVIDIA container runtime method to ensure backward compatibility.
* **Frequency**: These intensive hardware tests are triggered by changes to the workflow or Testflinger configurations, and otherwise run on a scheduled basis every five days.
