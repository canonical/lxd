#!/bin/bash

export PATH=../lxc:../lxd:$PATH

set -e

. ./remote.sh
. ./signoff.sh

sudo mkdir -p /var/lib/lxd
sudo chown $USER:$USER /var/lib/lxd
lxd --tcp 127.0.0.1:5555 &
lxd_pid=$!

alive=0
while [ $alive -eq 0 ]; do
  lxc ping && alive=1 || true
done

lxc config set password foo

test_commits_signed_off
test_remote

echo Success!
