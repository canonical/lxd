cert_fingerprint() {
  openssl x509 -noout -fingerprint -sha256 -in "${1}" | sed 's/.*=//; s/://g; s/\(.*\)/\L\1/'
}

# Generate a short lived ecdsa (self-signed) cert and key
# using the NIST curve P-384 and SHA384 hash for signature
# to match what LXD generates by default.
gen_cert_and_key() {
    key_file="${LXD_CONF}/${1}.key"
    crt_file="${LXD_CONF}/${1}.crt"
    cn="${1}.local"
    openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:secp384r1 -sha384 -keyout "${key_file}" -nodes -out "${crt_file}" -days 1 -subj "/CN=${cn}"
}

# Convert a certificate to YAML format for inclusion in LXD cluster preseed files.
cert_to_yaml() {
  # Prepend 4 spaces to each line for the cluster certificate field in LXD preseed YAML.
  sed 's/^/    /' "${1}"
}

# Convert a certificate to JSON format for inclusion in LXD API calls.
cert_to_json() {
  jq --exit-status --raw-input --slurp '.' "${1}"
}
