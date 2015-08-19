#!/bin/bash

set -xe
export DEBIAN_FRONTEND=noninteractive

which add-apt-repository || (sudo apt-get update ; sudo apt-get install -y software-properties-common)
sudo add-apt-repository ppa:ubuntu-lxc/lxd-git-master
sudo apt-get update
which go || sudo apt-get install -y golang

[ -e $HOME/go ] || mkdir -p $HOME/go

cat << 'EOF' | sudo tee /etc/profile.d/S99go.sh
export GOPATH=$HOME/go
export PATH=$PATH:$GOPATH/bin
EOF

