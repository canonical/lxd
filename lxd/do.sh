#!/bin/bash

set -e
set -x

go build

sudo killall lxd || true

#sudo rm -rf /var/lib/lxd/lxc/foo* || true
sudo ./lxd --debug --tcp 127.0.0.1:443
