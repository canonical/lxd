(benchmark-performance)=
# How to benchmark performance

```{youtube} https://www.youtube.com/watch?v=z_OKwO5TskA
```

The performance of your LXD server or cluster depends on a lot of different factors, ranging from the hardware, the server configuration, the selected storage driver and the network bandwidth to the overall usage patterns.

To find the optimal configuration, you should run benchmark tests to evaluate different setups.

LXD provides a benchmarking tool for this purpose.
This tool allows you to initialize or launch a number of containers and measure the time it takes for the system to create the containers.
If you run this tool repeatedly with different configurations, you can compare the performance and evaluate which is the ideal configuration.

## Get the tool

To get the `lxd-benchmark` tool, you can download a pre-built binary:

1. Download the `bin.linux.lxd-benchmark` tool ([`bin.linux.lxd-benchmark.aarch64`](https://github.com/canonical/lxd/releases/latest/download/bin.linux.lxd-benchmark.aarch64) or [`bin.linux.lxd-benchmark.x86_64`](https://github.com/canonical/lxd/releases/latest/download/bin.linux.lxd-benchmark.x86_64)) from the **Assets** section of the latest [LXD release](https://github.com/canonical/lxd/releases).
1. Save the binary as `lxd-benchmark` and make it executable (usually by running `chmod u+x lxd-benchmark`).

If you have `go` ({ref}`requirements-go`) installed, you can build the tool with the following command:

    go install github.com/canonical/lxd/lxd-benchmark@latest

## Run the tool

Run `lxd-benchmark [action]` to measure the performance of your LXD setup.

The benchmarking tool uses the current LXD configuration, but users of the snap must export the `LXD_DIR` variable for the configuration to be found:

      export LXD_DIR=/var/snap/lxd/common/lxd

If you want to use a different project, specify it with `--project`.

For all actions, you can specify the number of parallel threads to use (default is to use a dynamic batch size).
You can also choose to append the results to a CSV report file and label them in a certain way.

See `lxd-benchmark help` for all available actions and flags.

### Select an image

Before you run the benchmark, select what kind of image you want to use.

Local image
: If you want to measure the time it takes to create a container and ignore the time it takes to download the image, you should copy the image to your local image store before you run the benchmarking tool.

  To do so, run a command similar to the following and specify the fingerprint (for example, `2d21da400963`) of the image when you run `lxd-benchmark`:

      lxc image copy ubuntu:22.04 local:

  You can also assign an alias to the image and specify that alias (for example, `ubuntu`) when you run `lxd-benchmark`:

      lxc image copy ubuntu:22.04 local: --alias ubuntu

Remote image
: If you want to include the download time in the overall result, specify a remote image (for example, `ubuntu:22.04`).
  The default image that `lxd-benchmark` uses is the latest Ubuntu image (`ubuntu:`), so if you want to use this image, you can leave out the image name when running the tool.

### Create and launch containers

Run the following command to create a number of containers:

    lxd-benchmark init --count <number> <image>

Add `--privileged` to the command to create privileged containers.

For example:

```{list-table}
   :header-rows: 1

* - Command
  - Description
* - `lxd-benchmark init --count 10 --privileged`
  - Create ten privileged containers that use the latest Ubuntu image.
* - `lxd-benchmark init --count 20 --parallel 4 ubuntu-minimal:22.04`
  - Create 20 containers that use the Ubuntu Minimal 22.04 image, using four parallel threads.
* - `lxd-benchmark init 2d21da400963`
  - Create one container that uses the local image with the fingerprint `2d21da400963`.
* - `lxd-benchmark init --count 10 ubuntu`
  - Create ten containers that use the image with the alias `ubuntu`.

```

If you use the `init` action, the benchmarking containers are created but not started.
To start the containers that you created, run the following command:

    lxd-benchmark start

Alternatively, use the `launch` action to both create and start the containers:

    lxd-benchmark launch --count 10 <image>

For this action, you can add the `--freeze` flag to freeze each container right after it starts.
Freezing a container pauses its processes, so this flag allows you to measure the pure launch times without interference of the processes that run in each container after startup.

### Delete containers

To delete the benchmarking containers that you created, run the following command:

    lxd-benchmark delete

```{note}
You must delete all existing benchmarking containers before you can run a new benchmark.
```
