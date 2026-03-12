test_dns() {
  local zoneName="lxdtest-dns.example.net"
  local altZoneName="lxdtest-dns-alt.example.net"
  local DNS_ADDR="127.0.0.1"
  local DNS_PORT="8853"

  # Create the zone and add explicit A and AAAA records so the zone has content.
  lxc network zone create "${zoneName}"
  lxc network zone record create "${zoneName}" www
  lxc network zone record entry add "${zoneName}" www A 192.0.2.1 --ttl 300
  lxc network zone record entry add "${zoneName}" www AAAA 2001:db8::1 --ttl 600

  # Enable the DNS listener on localhost.
  lxc config set core.dns_address "${DNS_ADDR}:${DNS_PORT}"

  sub_test "Verify unsupported DNS query types return NOTIMP"
  # The DNS server only accepts AXFR, IXFR, and SOA; any other type returns NOTIMP.
  dig +noall +comments "@${DNS_ADDR}" -p "${DNS_PORT}" A "${zoneName}" | grep -wF 'NOTIMP'
  dig +noall +comments "@${DNS_ADDR}" -p "${DNS_PORT}" MX "${zoneName}" | grep -wF 'NOTIMP'

  sub_test "Verify queries for non-existent zones return NXDOMAIN"
  if ! dig +noall +comments "@${DNS_ADDR}" -p "${DNS_PORT}" soa "does-not-exist.example.net" | grep -wF 'status: NXDOMAIN'; then
    echo "ERROR: SOA for non-existent zone did not return NXDOMAIN, aborting" >&2
    exit 1
  fi

  sub_test "Verify zone access is denied without peer configuration"
  # All requests are denied when no peers are configured to avoid information leaks.
  if dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr "${zoneName}" | grep -wF 'SOA'; then
    echo "ERROR: AXFR without any peer configuration unexpectedly succeeded, aborting" >&2
    exit 1
  fi

  if ! dig +noall +comments "@${DNS_ADDR}" -p "${DNS_PORT}" soa "${zoneName}" | grep -wF 'status: NXDOMAIN'; then
    echo "ERROR: SOA without any peer configuration did not return NXDOMAIN, aborting" >&2
    exit 1
  fi

  sub_test "Verify IP-only peer grants SOA, AXFR, and IXFR access"
  lxc network zone set "${zoneName}" peers.ippeer.address=127.0.0.1

  dig +noall +comments "@${DNS_ADDR}" -p "${DNS_PORT}" soa "${zoneName}" | grep -wF 'NOERROR'

  local zoneXFR
  zoneXFR="$(dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr "${zoneName}")"
  grep -wF 'SOA' <<<"${zoneXFR}"
  # Confirm that the explicit A and AAAA records are visible in the zone transfer with their respective TTLs.
  grep -wF "www.${zoneName}." <<<"${zoneXFR}" | grep -E '[[:space:]]300[[:space:]]IN[[:space:]]A[[:space:]]192\.0\.2\.1$'
  grep -wF "www.${zoneName}." <<<"${zoneXFR}" | grep -E '[[:space:]]600[[:space:]]IN[[:space:]]AAAA[[:space:]]2001:db8::1$'

  # IXFR falls back to a full zone transfer; serial 0 requests all records.
  dig +tcp "@${DNS_ADDR}" -p "${DNS_PORT}" ixfr=0 "${zoneName}" | grep -wF 'SOA'

  sub_test "Verify IP restriction denies access from an unauthorized address"
  # Peer address restricted to 127.0.0.2; requests from 127.0.0.1 must be denied.
  lxc network zone unset "${zoneName}" peers.ippeer.address
  lxc network zone set "${zoneName}" peers.restrictedpeer.address=127.0.0.2

  if dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr "${zoneName}" | grep -wF 'SOA'; then
    echo "ERROR: AXFR from unauthorized IP address unexpectedly succeeded, aborting" >&2
    exit 1
  fi

  lxc network zone unset "${zoneName}" peers.restrictedpeer.address

  sub_test "Verify TSIG authentication: missing TSIG is denied when key is required"
  local tsigSecret
  tsigSecret="$(head -c 32 /dev/urandom | base64 -w 0)"
  local tsigKeyName="${zoneName}_tsigpeer."
  # Peer requires both a matching IP address and a valid TSIG key.
  lxc network zone set "${zoneName}" peers.tsigpeer.address=127.0.0.1 "peers.tsigpeer.key=${tsigSecret}"

  if dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr "${zoneName}" | grep -wF 'SOA'; then
    echo "ERROR: AXFR without TSIG unexpectedly succeeded when key is required, aborting" >&2
    exit 1
  fi

  sub_test "Verify TSIG authentication: wrong TSIG secret is denied"
  local wrongSecret
  wrongSecret="$(head -c 32 /dev/urandom | base64 -w 0)"

  if dig -y "hmac-sha256:${tsigKeyName}:${wrongSecret}" "@${DNS_ADDR}" -p "${DNS_PORT}" axfr "${zoneName}" | grep -wF 'SOA'; then
    echo "ERROR: AXFR with wrong TSIG secret unexpectedly succeeded, aborting" >&2
    exit 1
  fi

  sub_test "Verify TSIG authentication: valid TSIG key from a different zone is denied"
  # A key that passes TSIG verification but belongs to another zone should be
  # rejected by the key-name check in isAllowed to prevent cross-zone access.
  local altSecret
  altSecret="$(head -c 32 /dev/urandom | base64 -w 0)"
  local altTsigKeyName="${altZoneName}_altpeer."
  lxc network zone create "${altZoneName}"
  lxc network zone set "${altZoneName}" "peers.altpeer.key=${altSecret}"

  # altTsigKeyName is in the server's TSIG secret map (loaded from the alt zone),
  # so TsigStatus() returns nil (valid signature) — but the key name does not
  # match the expected name for the primary zone's peer, so access must be denied.
  if dig -y "hmac-sha256:${altTsigKeyName}:${altSecret}" "@${DNS_ADDR}" -p "${DNS_PORT}" axfr "${zoneName}" | grep -wF 'SOA'; then
    echo "ERROR: AXFR with cross-zone TSIG key unexpectedly succeeded, aborting" >&2
    exit 1
  fi

  sub_test "Verify TSIG authentication: correct TSIG key and secret grants AXFR access"
  zoneXFR="$(dig -y "hmac-sha256:${tsigKeyName}:${tsigSecret}" "@${DNS_ADDR}" -p "${DNS_PORT}" axfr "${zoneName}")"
  grep -wF 'SOA' <<<"${zoneXFR}"
  grep -wF "www.${zoneName}." <<<"${zoneXFR}" | grep -E '[[:space:]]300[[:space:]]IN[[:space:]]A[[:space:]]192\.0\.2\.1$'
  grep -wF "www.${zoneName}." <<<"${zoneXFR}" | grep -E '[[:space:]]600[[:space:]]IN[[:space:]]AAAA[[:space:]]2001:db8::1$'
  # Confirm that the response carries a verified TSIG record for the expected key.
  grep -wF "${tsigKeyName}" <<<"${zoneXFR}" | grep -wF 'TSIG' | grep -wF 'NOERROR'
  # IXFR (requires TCP) with a TSIG-signed request; serial 0 triggers a full zone transfer.
  dig +tcp -y "hmac-sha256:${tsigKeyName}:${tsigSecret}" "@${DNS_ADDR}" -p "${DNS_PORT}" ixfr=0 "${zoneName}" | grep -wF 'SOA'

  sub_test "Verify DNS listener survives address reconfiguration"
  # Switch to a different port and back to force two restarts of the listener.
  local DNS_PORT_ALT="8854"
  lxc config set core.dns_address "${DNS_ADDR}:${DNS_PORT_ALT}"
  lxc config set core.dns_address "${DNS_ADDR}:${DNS_PORT}"
  dig -y "hmac-sha256:${tsigKeyName}:${tsigSecret}" "@${DNS_ADDR}" -p "${DNS_PORT}" axfr "${zoneName}" | grep -wF 'SOA'

  # Cleanup.
  lxc network zone delete "${altZoneName}"
  lxc network zone delete "${zoneName}"
  lxc config unset core.dns_address
}
