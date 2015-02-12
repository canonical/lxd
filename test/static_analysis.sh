safe_pot_hash() {
  grep ^msgid $toplevel/po/lxd.pot | md5sum | cut -f1 -d" "
}

static_analysis() {
  toplevel="$(git rev-parse --show-toplevel)"

  # go tools
  (cd $toplevel && go fmt ./... && go vet ./...)
  git diff --exit-code

  # make sure the .pot is updated
  hash1=$(safe_pot_hash)
  (cd $toplevel && make i18n)
  hash2=$(safe_pot_hash)
  if [ "$hash1" != "$hash2" ]; then
    echo "please update the .pot file in your commit (make i18n)" && false
  fi
}
