#!/bin/bash
set -eu
set -o pipefail
shopt -s inherit_errexit

zerolint ./...
