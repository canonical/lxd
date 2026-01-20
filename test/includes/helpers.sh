# uuidgen: Generate a random UUID
uuidgen() {
  echo "$(< /proc/sys/kernel/random/uuid)"
}
