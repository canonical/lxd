#!/bin/sh -eu

if ! command -v mdl >/dev/null; then
    echo "Install mdl with 'snap install mdl' first."
    exit 1
fi

trap "rm -rf .tmp/" EXIT

## Preprocessing

for fn in $(find doc/ -name '*.md'); do
    mkdir -p $(dirname ".tmp/$fn");
    sed -E "s/(\(.+\)=)/\1\n/" $fn > .tmp/$fn;
done

mdl .tmp/doc -sdoc/.sphinx/.markdownlint/style.rb -udoc/.sphinx/.markdownlint/rules.rb --ignore-front-matter > .tmp/errors.txt || true

## Postprocessing

filtered_errors="$(grep -vxFf doc/.sphinx/.markdownlint/exceptions.txt .tmp/errors.txt)"
if [ "$(echo "$filtered_errors" | wc -l)" = "2" ]; then
    echo "Passed!"
    exit 0
else
    echo "Failed!"
    echo "$filtered_errors"
    exit 1
fi
