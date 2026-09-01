# Testflinger scripts

This directory contains the scripts used for GPU testing (NVIDIA and AMD) via Github actions and Testflinger.
The tests run on devices within Canonical's test farm.

## Run locally
Running the tests locally is only possible if your machine has access to the Testflinger server.

Tested distros:
- `core24-latest`
- `jammy`
- `noble`

Current queue/workflow combinations:
- NVIDIA (CDI and legacy runtime): `JOB_QUEUE=lxd-nvidia`, `TFWORKFLOW=nvidia-gpu-job`
- NVIDIA MIG (Multi-Instance GPU): `JOB_QUEUE=lxd-mig`, `TFWORKFLOW=nvidia-mig-job`
- Ubuntu Core + NVIDIA CDI: `JOB_QUEUE=lxd-nvidia`, `TFWORKFLOW=uc-nvidia-cdi-job`
- AMD CDI: `JOB_QUEUE=lxd-amd`, `TFWORKFLOW=amd-cdi-job`

Ensure `testflinger` is installed:
```
sudo snap install testflinger-cli
```

Set the input variables and execute the script from the same directory:
```bash
JOB_QUEUE=lxd-nvidia SNAP_CHANNEL=latest/edge DISTRO=core24-latest TFWORKFLOW=uc-nvidia-cdi-job ./run.sh
```
The above replaces the inputs in the scripts and submits the Testflinger job.
To prepare the scripts only but not submit the job, set the `--dryrun` flag.

## Examples

Notice, that some Testflinger workflows are only compatible with `core24-latest`, while others only
with classic Ubuntu images.

To test Ubuntu Core + LXD + GPU passthrough in CDI mode:
```bash
JOB_QUEUE=lxd-nvidia SNAP_CHANNEL=latest/edge DISTRO=core24-latest TFWORKFLOW=uc-nvidia-cdi-job ./run.sh
```

To test Ubuntu Noble + LXD + GPU passthrough (CDI and legacy nvidia runtime):
```bash
JOB_QUEUE=lxd-nvidia SNAP_CHANNEL=latest/edge DISTRO=noble TFWORKFLOW=nvidia-gpu-job ./run.sh
```

To test Ubuntu Noble + LXD + AMD GPU passthrough in CDI mode:
```bash
JOB_QUEUE=lxd-amd SNAP_CHANNEL=latest/edge DISTRO=noble TFWORKFLOW=amd-cdi-job ./run.sh
```

To test Ubuntu Noble + LXD + NVIDIA MIG (Multi-Instance GPU) passthrough:
```bash
JOB_QUEUE=lxd-mig SNAP_CHANNEL=latest/edge DISTRO=noble TFWORKFLOW=nvidia-mig-job ./run.sh
```
