#!/bin/bash

export PATH=../lxc:../lxd:$PATH

set -e

. ./remote.sh
. ./signoff.sh

test_commits_signed_off
test_remote

echo Success!
