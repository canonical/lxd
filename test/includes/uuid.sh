# shellcheck shell=sh
is_uuid_v4() {
  # Case insensitive match for a v4 UUID. The third group must start with 4, and the fourth group must start with 8, 9,
  # a, or b. This accounts for the version and variant. See https://datatracker.ietf.org/doc/html/rfc9562#name-uuid-version-4.
  printf '%s' "${1}" | grep --ignore-case '^[0-9a-f]\{8\}-[0-9a-f]\{4\}-4[0-9a-f]\{3\}-[89ab][0-9a-f]\{3\}-[0-9a-f]\{12\}$'
}