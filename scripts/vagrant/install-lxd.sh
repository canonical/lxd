#!/bin/bash

set -xe
export DEBIAN_FRONTEND=noninteractive

# install runtime dependencies
sudo apt-get -y install xz-utils tar acl curl gettext \
    jq sqlite3

# install build dependencies
sudo apt-get -y install lxc lxc-dev mercurial git pkg-config \
    protobuf-compiler golang-goprotobuf-dev

# setup env 
[ -e uid_gid_setup ] || \
    echo "root:1000000:65536" | sudo tee -a /etc/subuid /etc/subgid && \
    touch uid_gid_setup


go get github.com/lxc/lxd
cd $GOPATH/src/github.com/lxc/lxd
go get -v -d ./...
make


cat << 'EOF' | sudo tee /etc/init/lxd.conf
description "LXD daemon"
author      "John Brooker"

start on filesystem or runlevel [2345]
stop on shutdown

script

    exec /home/vagrant/go/bin/lxd --group vagrant

end script

EOF

sudo service lxd start
