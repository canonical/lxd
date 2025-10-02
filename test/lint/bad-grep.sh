#!/bin/bash
set -eu
set -o pipefail

RC=0

# `grep -q` should be avoided in pipelines when using `set -o pipefail` (and
# `set -e`) as when the first pattern match occurs, `grep` terminates leading
# to a `SIGPIPE` to be sent to the process writing into the pipe. If that
# command does not gracefully handle `SIGPIPE`, it will error out causing the
# whole pipeline to fail.
# XXX: the `echo` (builtin) command gracefully handles `SIGPIPE` so it is
# permitted.
OUTPUT="$(grep --exclude-dir=lint -rE ' ?\| ?grep -[^ ]*q' test/ | grep -vE 'echo [^ ]+ \| grep -[^ ]*q' || true)"
if [ -n "${OUTPUT}" ]; then
    echo "FAIL: avoid using 'grep -q' in command pipelines with 'set -o pipefail'"
    echo "${OUTPUT}"
    echo
    RC=1
fi

# `grep -v` should not be used **at the end** of pipelines to verify if a
# pattern is or isn't present as the return code does not reflect if the
# pattern was found and suppressed or not found:
#
#   ```
#   $ echo -e 'a\nb' | grep -v a; echo $?
#   b
#   0
#
#   $ echo -e 'a\nb' | grep -v c; echo $?
#   a
#   b
#   0
#   ```
# XXX: this search pattern is not perfect but should catch most use of
# `grep -v` at the end of pipelines and when not used inside a shell
# comparison.
OUTPUT="$(grep --exclude-dir=lint -rE ' ?\| ?grep -v[^(\|=>)]+$' test/ || true)"
if [ -n "${OUTPUT}" ]; then
    echo "FAIL: unreliable use of 'grep -v' at the end of command pipelines"
    echo "${OUTPUT}"
    echo
    RC=1
fi

exit "${RC}"
