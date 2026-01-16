# LXD testing

The testing of LXD is split across two primary repositories: [`canonical/lxd`](https://github.com/canonical/lxd), which handles core code tests and functional integration, and [`canonical/lxd-ci`](https://github.com/canonical/lxd-ci), which focuses on distribution-specific image building and snap integration tests ran on schedule and against supported snap channels.

## `canonical/lxd`

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
        O1[deps - liblxc/dqlite/minio]
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
        UnitTests --> PrimeCaches[Prime caches: External images, snaps & minio]
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

The **`snap-tests`** job validates LXD's behavior as a snap package across different Ubuntu releases, utilizing test scripts and infrastructure from the `canonical/lxd-ci` repository.

* **External Integration**: Unlike other jobs, this specifically checkouts the `canonical/lxd-ci` repository to reuse its specialized testing scripts.
* **Infrastructure Preparation**: Dynamically configures **MicroCeph** and **MicroOVN** to provide clustered storage and networking services needed for the snap-based integration tests.
* **Test Adaptation**: Customizes the `lxd-ci` environment on the fly to "sideload" the LXD snap built during the current run, ensuring the CI tests the exact code changes being proposed.
* **Matrix Dimensions**:
  * **OS**: Validates compatibility across multiple Ubuntu releases (e.g., `22.04`, `24.04`).
  * **Test Suites**: Runs over 35 specific functional tests, including `cloud-init`, `network-ovn`, `vm-migration`, and various `storage-vm` backends.
* **Resource Management**: Includes aggressive memory and disk space reclamation steps to ensure the GitHub runner has enough headroom for intensive virtual machine tests.

```mermaid
graph TD
    subgraph ST_Matrix [Matrix strategy]
        direction LR
        O_Node{OS}
        S_Node{Suite}

        O_Node --- O1[ubuntu-22.04]
        O_Node --- O2[ubuntu-24.04]

        S_Node --- S1[cgroup]
        S_Node --- S2[cloud-init]
        S_Node --- S3[cluster]
        S_Node --- S4[container]
        S_Node --- S5_32[...]
        S_Node --- S33[vm]
        S_Node --- S34[vm-nesting]
        S_Node --- S35[vm-migration]
    end
    subgraph ST [Snap tests]
        direction TB
        Start(["**Dimensions**: **OS** x **test suite**"]) --> PullArtifacts[Download dependencies: dqlite, images, snaps, & LXD binaries]

        PullArtifacts --> InfraCheck{Setup MicroCeph?}
        InfraCheck -- Yes --> SetupCeph[Setup MicroCeph 3-node cluster]
        InfraCheck -- No --> SetupOVN[Setup MicroOVN]
        SetupCeph --> SetupOVN

        SetupOVN --> AdaptTests[Adapt lxd-ci tests: Sideload built snap & enable coverage]
        AdaptTests --> ExecTest[Execute matrix.test via lxd-ci local-run]

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
        InstallDeps --> PullArtifacts[Download images, snaps, minio & binaries]

        PullArtifacts --> CephCheck{Is backend ceph or all?}
        CephCheck -- Yes --> SetupCeph[Setup MicroCeph 3-node cluster]
        CephCheck -- No --> SetupOVN[Setup MicroOVN]
        SetupCeph --> SetupOVN

        SetupOVN --> EnvSetup[System Environment Setup]
        EnvSetup --> ExecTest[Execute main.sh group:GROUP for assigned backend]

        ExecTest --> Uploads[Upload crash dumps & coverage data]
    end
```

## `canonical/lxd-ci`

Most of the test happens on GitHub-hosted runners that are `amd64` based runners. Some tests however require access to specialized hardware and for those, **Testflinger** runners are used.

### Snap integration tests

The **`system-tests`** job executes a massive matrix of functional tests across multiple LXD snap tracks and Ubuntu releases.

* **Matrix Strategy**: This job runs a three-dimensional matrix combining two Ubuntu versions (`22.04`, `24.04`), four snap tracks (`latest/edge`, `6/edge`, `5.21/edge`, `5.0/edge`), and 35 distinct test suites.
* **Specialized Infrastructure**:
  * **MicroCeph**: Setup for storage-related tests (e.g., `storage-vm ceph`).
  * **VM Caching**: Specifically handles external VM images for the `qemu-external-vm` suite to speed up execution.
  * **Node.js**: Installed only when executing browser-based UI tests.
* **Test Execution**: Uses the `./bin/local-run` helper script to trigger the specific logic for each test suite defined in the matrix.
* **Release Logic**: Includes specific logic to exclude incompatible combinations, such as skipping `lxd-installer` tests on older snap tracks or older Ubuntu releases.

```mermaid
graph TD
    subgraph ST_Matrix [Matrix strategy]
        direction LR
        C_Node{Channel}
        O_Node{OS}
        S_Node{Suite}

        C_Node --- C1[latest/edge]
        C_Node --- C2[6/edge]
        C_Node --- C3[5.21/edge]
        C_Node --- C4[5.0/edge]

        O_Node --- O1[ubuntu-22.04]
        O_Node --- O2[ubuntu-24.04]

        S_Node --- S1[cgroup]
        S_Node --- S2[cloud-init]
        S_Node --- S3[cluster]
        S_Node --- S4[container]
        S_Node --- S5_33[...]
        S_Node --- S34[ui chromium]
        S_Node --- S35[ui firefox]
        S_Node --- S36[vm]
        S_Node --- S37[vm-nesting]
        S_Node --- S38[vm-migration]
    end
    subgraph ST [Snap tests]
        direction TB
        Start(["**Dimensions**: **OS** x **test suite**"]) --> PullArtifacts[Download dependencies: dqlite, images, snaps, & LXD binaries]

        PullArtifacts --> InfraCheck{Setup MicroCeph?}
        InfraCheck -- Yes --> SetupCeph[Setup MicroCeph 3-node cluster]
        InfraCheck -- No --> SetupOVN[Setup MicroOVN]
        SetupCeph --> SetupOVN

        SetupOVN --> AdaptTests[Adapt lxd-ci tests: Sideload built snap & enable coverage]
        AdaptTests --> ExecTest[Execute matrix.test via lxd-ci local-run]

        ExecTest --> Uploads[Upload reports, crash dumps, & coverage data]
    end
```

### GPU passthrough tests

This workflow is a pipeline that automates the validation of NVIDIA GPU passthrough for containers using specialized hardware.

* **Specialized Infrastructure**: These tests execute on **self-hosted** runners due to needing to launch jobs via [Testflinger](https://canonical-testflinger.readthedocs-hosted.com/latest/) that is not accessible from GitHub-hosted runners.
* **Testflinger Integration**: The workflow does not run the tests directly on the **self-hosted** runner; instead, it uses the `canonical/testflinger` action to submit jobs to a dedicated hardware queue ([`lxd-nvidia`](https://testflinger.canonical.com/queues/lxd-nvidia)) with a set of physical machine with the needed hardware available.
* **Test Variants**:
  * **CDI Tests**: Validates the modern **Container Device Interface** (CDI) passthrough on both Ubuntu Core 24 and standard Ubuntu releases.
  * **Legacy Runtime**: Validates the older NVIDIA container runtime method to ensure backward compatibility.
* **Frequency**: These intensive hardware tests are triggered by changes to the workflow or Testflinger configurations, and otherwise run on a scheduled basis every five days.
