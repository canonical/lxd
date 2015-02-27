safe_pot_hash() {
  grep ^msgid $toplevel/po/lxd.pot | md5sum | cut -f1 -d" "
}

static_analysis() {
  toplevel="$(git rev-parse --show-toplevel)"

  # go tools
  git add -u :/
  (cd $toplevel && go fmt ./... && go vet ./...)
  git diff --exit-code

  # make sure the .pot is updated
  cp $toplevel/po/lxd.pot $toplevel/po/lxd.pot.bak
  hash1=$(safe_pot_hash)
  (cd $toplevel && make i18n -s)
  hash2=$(safe_pot_hash)
  mv $toplevel/po/lxd.pot.bak $toplevel/po/lxd.pot
  if [ "$hash1" != "$hash2" ]; then
    echo "==> Please update the .pot file in your commit (make i18n)" && false
  fi
}
