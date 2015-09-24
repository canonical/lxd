test_commits_signed_off() {
  # Skip the test when not running from a git repository
  if ! git status; then
    return
  fi

  # Don't run this test if we're not in travis; we don't want to muck with
  # people's local repos.
  if [ -z "${TRAVIS_PULL_REQUEST:-}" ]; then
    return
  fi

  git remote add lxc https://github.com/lxc/lxd
  git fetch lxc master
  for i in $(git cherry lxc/master | grep '^+' | cut -d' ' -f2); do
    git show "${i}" | grep -q 'Signed-off-by' || \
        ( echo "==> Commit without sign-off:" ; git show "${i}" ; false )
  done
  git remote remove lxc
}
