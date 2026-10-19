#!/bin/bash
# Check that the documentation redirects are in place.
#
# VERSION_REDIRECTS: entries on the pattern "<from> <status> <to>", where "from"
# and "to" are the paths relative to the docs base URL (without leading or
# trailing slashes). For each entry, the root URL with that version and the
# "images" page (exists in every version) are checked for a redirect with a code
# 301 or 302. Destinations also checked to ensure they resolve with 200.
#
# URL_REDIRECTS: entries on the pattern "<source URL> <status> <target URL>" and
# checked as given. Destination URLs checked to ensure they resolve with 200.

set -u

BASE="https://canonical.com/lxd/docs"

VERSION_REDIRECTS=(
    # Versions with permanent redirects from branch name to "v{version}"
    "stable-4.0 301 v4"
    "stable-5.0 301 v5"
    "stable-5.21 301 v5.21"
    # Most recent LTS with temporary redirect from "v{version}" to "default"
    "v6 302 default"
    # Redirects from versions with translations
    "en/latest 301 latest"
)

URL_REDIRECTS=(
    # Redirects from old domain to new domain
    "https://documentation.ubuntu.com/lxd 301 https://canonical.com/lxd/docs"
    "https://documentation.ubuntu.com/lxd/latest/ 301 https://canonical.com/lxd/docs/latest/"
)

failures=0
checks=0

fail() {
    echo "FAIL: $*"
    failures=$((failures + 1))
}

# check_redirect <url> <expected status> <expected absolute location>
check_redirect() {
    local url="$1" want_code="$2" want_loc="$3" out code loc
    checks=$((checks + 1))
    out="$(curl --silent --show-error -o /dev/null --retry 3 --retry-all-errors --max-time 30 -w '%{http_code} %{redirect_url}' "${url}")"
    code="${out%% *}"
    loc="${out#* }"
    if [ "${code}" = "${want_code}" ] && [ "${loc}" = "${want_loc}" ]; then
        echo "ok:   ${url} -> ${code} ${loc}"
    else
        fail "${url}: expected ${want_code} ${want_loc}, got ${code} ${loc}"
    fi
}

# check_ok <url>: the final page (after following redirects) must exist
check_ok() {
    local url="$1" code
    checks=$((checks + 1))
    code="$(curl --silent --show-error -L -o /dev/null --retry 3 --retry-all-errors --max-time 30 -w '%{http_code}' "${url}")"
    if [ "${code}" = "200" ]; then
        echo "ok:   ${url} status ${code}"
    else
        fail "${url}: expected final status 200, got ${code}"
    fi
}

for entry in "${VERSION_REDIRECTS[@]}"; do
    read -r from code to <<<"${entry}"
    check_redirect "${BASE}/${from}/" "${code}" "${BASE}/${to}/"
    # Check a page that exists across docs versions
    check_redirect "${BASE}/${from}/images/" "${code}" "${BASE}/${to}/images/"
    check_ok "${BASE}/${to}/"
    check_ok "${BASE}/${to}/images/"
done

for entry in "${URL_REDIRECTS[@]}"; do
    read -r from code to <<<"${entry}"
    check_redirect "${from}" "${code}" "${to}"
    check_ok "${to}"
done

echo
echo "${checks} checks, ${failures} failed"
[ "${failures}" -eq 0 ]
