# uuidgen: Generate a random UUID
uuidgen() {
  echo "$(< /proc/sys/kernel/random/uuid)"
}

# Wait for a background process to finish.
# Treats exit code 127 (PID not known to the shell, i.e. not a child process or
# already reaped) as success to handle the case where the process exited and was
# reaped before wait was called. Any other non-zero exit code causes a global failure.
wait_pid() {
  local pid="$1"
  local rc

  # Validate that a non-empty numeric PID was provided to avoid masking bugs
  # where wait returns 127 due to an invalid argument.
  [[ "${pid}" =~ ^[0-9]+$ ]] || { echo "wait_pid: invalid PID: '${pid}'" >&2; exit 1; }

  wait "${pid}" || { rc=$?; [ "${rc}" -eq 127 ] || exit "${rc}"; }
}
