#!/bin/bash -eu

temp_job="${TFWORKFLOW}.yml.tmp"
LXD_REF="${LXD_REF:-main}"

echo "Inputs: $JOB_QUEUE $DISTRO $SNAP_CHANNEL $TFWORKFLOW"

# Replace env vars with inputs
# shellcheck disable=SC2016 # envsubst requires literal variable names.
envsubst '$JOB_QUEUE $DISTRO $SNAP_CHANNEL $LXD_REF' < "${TFWORKFLOW}.yml" > "${temp_job}"

if [[ "${1:-}" == "--dryrun" ]]; then
  echo "Dry-run complete"
  echo "Submit the job with:"
  echo "testflinger submit --poll $temp_job"
  exit 0
fi

# Submit the modified job
testflinger submit --poll "${temp_job}"
