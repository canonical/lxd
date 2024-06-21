(container-gpu-passthrough-with-docker)=
# How to pass an NVIDIA GPU to a container

If you have an NVIDIA GPU (either discrete (dGPU) or integrated (iGPU)) and you want to pass the runtime libraries and configuration installed on your host to your container, you should add a {ref}`LXD GPU device <devices-gpu>`.
Consider the following scenario:

Your host is an NVIDIA single board computer that has a Tegra SoC with an iGPU, and you have the Tegra SDK installed on the host. You want to create a LXD container and run an application inside the container using the iGPU as a compute backend. You want to run this application inside a Docker container (or another OCI-compliant runtime).
To achieve this, complete the following steps:

1. Running a Docker container inside a LXD container can potentially consume a lot of disk space if the outer container is not well configured. Here are two options you can use to optimize the consumed disk space:

    - Either you create a BTRFS storage pool to back the LXD container so that the Docker image later used does not use the VFS storage driver which is very space inefficient, then you initialize the LXD container with {config:option}`instance-security:security.nesting` enabled (needed for running a Docker container inside a LXD container) and using the BTRFS storage pool:

          lxc storage create p1 btrfs size=15GiB
          lxc init ubuntu:24.04 t1 --config security.nesting=true -s p1

    - Or you use the `overlayFS` storage driver in Docker but you need to specify the following syscall interceptions, still with the {config:option}`instance-security:security.nesting` enabled:

          lxc init ubuntu:24.04 t1 --config security.nesting=true --config security.syscalls.intercept.mknod=true --config security.syscalls.intercept.setxattr=true

1. Add the GPU device to your container:

    - If you want to do an iGPU pass-through:

          lxc config device add t1 igpu0 gpu gputype=physical id=nvidia.com/igpu=0

    - If you want to do a dGPU pass-through:

          lxc config device add t1 gpu0 gpu gputype=physical id=nvidia.com/gpu=0

After adding the device, let's try to run a basic [MNIST](https://en.wikipedia.org/wiki/MNIST_database) inference job inside our LXD container.

1. Create a `cloud-init` script that installs the Docker runtime, the [NVIDIA Container Toolkit](https://github.com/NVIDIA/nvidia-container-toolkit), and a script to run a test [TensorRT](https://github.com/NVIDIA/TensorRT) workload:

        #cloud-config
        package_update: true
        write_files:
          # `run_tensorrt.sh` compiles samples TensorRT applications and run the the `sample_onnx_mnist` program which loads an ONNX model into the TensorRT inference server and execute a digit recognition job.
          - path: /root/run_tensorrt.sh
            permissions: "0755"
            owner: root:root
            content: |
              #!/bin/bash
              echo "OS release,Kernel version"
              (. /etc/os-release; echo "${PRETTY_NAME}"; uname -r) | paste -s -d,
              echo
              nvidia-smi -q
              echo
              exec bash -o pipefail -c "
              cd /workspace/tensorrt/samples
              make -j4
              cd /workspace/tensorrt/bin
              ./sample_onnx_mnist
              retstatus=\${PIPESTATUS[0]}
              echo \"Test exited with status code: \${retstatus}\" >&2
              exit \${retstatus}
              "
        runcmd:
          # Install Docker to run the AI workload
          - curl -fsSL https://get.docker.com -o install-docker.sh
          - sh install-docker.sh --version 24.0
          # The following installs the NVIDIA container toolkit
          # as explained in the official doc website: https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html#installing-with-apt
          - curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | gpg
            --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
          - curl -fsSL https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list | sed -e 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' -e '/experimental/ s/^#//g' | tee /etc/apt/sources.list.d/nvidia-container-toolkit.list
          # Now that an new apt source/key was added, update the package definitions.
          - apt-get update
          # Install NVIDIA container toolkit
          - DEBIAN_FRONTEND=noninteractive apt-get install -y nvidia-container-toolkit
          # Ultimately, we need to tell Docker, our container runtime, to use `nvidia-ctk` as a runtime.
          - nvidia-ctk runtime configure --runtime=docker
            --config=/etc/docker/daemon.json
          - systemctl restart docker

1. Apply this `cloud-init` setup to your instance:

        lxc config set t1 cloud-init.user-data - < cloud-init.yml

1. Start the instance:

        lxc start t1

1. Wait for the `cloud-init` process to finish:

        lxc exec t1 -- cloud-init status --wait

1. Once `cloud-init` is finished, open a shell in the instance:

        lxc exec t1 -- bash

1. Edit the NVIDIA container runtime to avoid using `cgroups`:

        sudo nvidia-ctk config  --in-place --set nvidia-container-cli.no-cgroups

1. If you use an iGPU and your NVIDIA container runtime is not automatically enabled with CSV mode (needed for NVIDIA Tegra board), enable it manually:

        sudo nvidia-ctk config --in-place --set nvidia-container-runtime.mode=csv

1. Now, run the inference workload with Docker:

    - If you set up a dGPU pass-through:

          docker run --gpus all --runtime nvidia --rm -v $(pwd):/sh_input nvcr.io/nvidia/tensorrt:24.02-py3 bash /sh_input/run_tensorrt.sh

    - If you set up an iGPU pass-through:

          docker run --gpus all --runtime nvidia --rm -v $(pwd):/sh_input nvcr.io/nvidia/tensorrt:24.02-py3-igpu bash /sh_input/run_tensorrt.sh

  In the end you should see something like:

        @@@@@@@@@@@@@@@@@@@@@@@@@@@@
        @@@@@@@@@@@@@@@@@@@@@@@@@@@@
        @@@@@@@@@@@@@@@@@@@@@@@@@@@@
        @@@@@@@@@@@@@@@@@@@@@@@@@@@@
        @@@@@@@@@@=   ++++#++=*@@@@@
        @@@@@@@@#.            *@@@@@
        @@@@@@@@=             *@@@@@
        @@@@@@@@.   .. ...****%@@@@@
        @@@@@@@@: .%@@#@@@@@@@@@@@@@
        @@@@@@@%  -@@@@@@@@@@@@@@@@@
        @@@@@@@%  -@@*@@@*@@@@@@@@@@
        @@@@@@@#  :#- ::. ::=@@@@@@@
        @@@@@@@-             -@@@@@@
        @@@@@@%.              *@@@@@
        @@@@@@#     :==*+==   *@@@@@
        @@@@@@%---%%@@@@@@@.  *@@@@@
        @@@@@@@@@@@@@@@@@@@+  *@@@@@
        @@@@@@@@@@@@@@@@@@@=  *@@@@@
        @@@@@@@@@@@@@@@@@@*   *@@@@@
        @@@@@%+%@@@@@@@@%.   .%@@@@@
        @@@@@*  .******=    -@@@@@@@
        @@@@@*             .#@@@@@@@
        @@@@@*            =%@@@@@@@@
        @@@@@@%#+++=     =@@@@@@@@@@
        @@@@@@@@@@@@@@@@@@@@@@@@@@@@
        @@@@@@@@@@@@@@@@@@@@@@@@@@@@
        @@@@@@@@@@@@@@@@@@@@@@@@@@@@

        [07/31/2024-13:19:21] [I] Output:
        [07/31/2024-13:19:21] [I]  Prob 0  0.0000 Class 0:
        [07/31/2024-13:19:21] [I]  Prob 1  0.0000 Class 1:
        [07/31/2024-13:19:21] [I]  Prob 2  0.0000 Class 2:
        [07/31/2024-13:19:21] [I]  Prob 3  0.0000 Class 3:
        [07/31/2024-13:19:21] [I]  Prob 4  0.0000 Class 4:
        [07/31/2024-13:19:21] [I]  Prob 5  1.0000 Class 5: **********
        [07/31/2024-13:19:21] [I]  Prob 6  0.0000 Class 6:
        [07/31/2024-13:19:21] [I]  Prob 7  0.0000 Class 7:
        [07/31/2024-13:19:21] [I]  Prob 8  0.0000 Class 8:
        [07/31/2024-13:19:21] [I]  Prob 9  0.0000 Class 9:
        [07/31/2024-13:19:21] [I]
        &&&& PASSED TensorRT.sample_onnx_mnist [TensorRT v8603] # ./sample_onnx_mnist
