test_docker() {
  if [ -n "${LXD_OFFLINE:-}" ]; then
    echo "LXD is not connected to the internet. Skipping..."
    return
  fi

  lxc launch ubuntu:xenial docker1
  # Give time to connect to network
  sleep 5s

  lxc exec docker1 -- apt update --yes --force-yes
  lxc exec docker1 -- apt install docker.io --yes --force-yes
  lxc exec docker1 -- systemctl stop docker.service
  lxc exec docker1 -- systemctl stop docker.socket

  # Download binaries built from current git head of the Docker repo.
  lxc exec docker1 -- wget https://master.dockerproject.org/linux/amd64/docker
  lxc exec docker1 -- wget https://master.dockerproject.org/linux/amd64/dockerd
  lxc exec docker1 -- wget https://master.dockerproject.org/linux/amd64/docker-containerd
  lxc exec docker1 -- wget https://master.dockerproject.org/linux/amd64/docker-containerd-shim
  lxc exec docker1 -- wget https://master.dockerproject.org/linux/amd64/docker-init
  lxc exec docker1 -- wget https://master.dockerproject.org/linux/amd64/docker-proxy
  lxc exec docker1 -- wget https://master.dockerproject.org/linux/amd64/docker-runc

  # client
  lxc exec docker1 -- cp docker /usr/bin/docker
  lxc exec docker1 -- chmod +x /usr/bin/docker

  # daemon
  lxc exec docker1 -- cp dockerd /usr/bin/dockerd
  lxc exec docker1 -- chmod +x /usr/bin/dockerd

  # another daemon
  lxc exec docker1 -- cp docker-containerd /usr/bin/docker-containerd
  lxc exec docker1 -- chmod +x /usr/bin/docker-containerd

  # another binary
  lxc exec docker1 -- cp docker-containerd-shim /usr/bin/docker-containerd-shim
  lxc exec docker1 -- chmod +x /usr/bin/docker-containerd-shim

  # yet another binary
  lxc exec docker1 -- cp docker-init /usr/bin/docker-init
  lxc exec docker1 -- chmod +x /usr/bin/docker-init

  # yet yet another binary
  lxc exec docker1 -- cp docker-proxy /usr/bin/docker-proxy
  lxc exec docker1 -- chmod +x /usr/bin/docker-proxy

  # yet yet yet another binary
  lxc exec docker1 -- cp docker-runc /usr/sbin/docker-runc
  lxc exec docker1 -- chmod +x /usr/sbin/docker-runc

  lxc exec docker1 -- systemctl start docker
  # Check if the Docker daemon successfully started and is active.
  [ "$(lxc exec docker1 -- systemctl is-active docker)" = "active" ]

  # Test whether we can pull a simple Docker image.
  lxc exec docker1 -- docker pull busybox:latest

  # Test whether we can remove a simple Docker image.
  lxc exec docker1 -- docker rmi busybox:latest

  lxc delete -f docker1
}
