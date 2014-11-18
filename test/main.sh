#!/bin/bash

export PATH=../lxc:../lxd:$PATH

set -e

. ./remote.sh
. ./signoff.sh

test_commits_signed_off
# temporarily don't run these tests.  We need to be able to
# run lxd under travis so we can talk to it.
#test_remote

echo Success!
