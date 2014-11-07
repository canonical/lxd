#!/bin/bash

export PATH=../lxc:../lxd:$PATH

set -e

. ./remote.sh

test_remote

echo Success!
