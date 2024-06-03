#!/bin/sh -eu

if ! command -v mdl >/dev/null; then
    echo "Install mdl with 'snap install mdl' first."
    exit 1
fi

trap "rm -rf .tmp/" EXIT

## Preprocessing

for fn in $(find . -name '*.md'); do
    mkdir -p $(dirname ".tmp/doc/$fn");
    sed -E "s/(\(.+\)=)/\1\n/" $fn > .tmp/doc/$fn;
done

rm -rf .tmp/doc/reference/manpages/

mdl .tmp/doc -s.sphinx/.markdownlint/style.rb -u.sphinx/.markdownlint/rules.rb --ignore-front-matter > .tmp/errors.txt || true

## Postprocessing

sed -i '/^$/,$d' .tmp/errors.txt

# Check for unneeded exceptions
unneeded_exceptions="$(grep -vxFf .tmp/errors.txt .sphinx/.markdownlint/exceptions.txt)" || true
if [ -n "${unneeded_exceptions}" ]; then
    echo "Failed due to unneeded exceptions rules!"
    echo "${unneeded_exceptions}"
    exit 1
fi

filtered_errors="$(grep -vxFf .sphinx/.markdownlint/exceptions.txt .tmp/errors.txt)" || true

if [ -z "$filtered_errors" ]; then
    echo "Passed!"
    exit 0
else
    echo "Failed!"
    echo "$filtered_errors"
    exit 1
fi
