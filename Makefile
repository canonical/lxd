DOMAIN=lxd
POFILES=$(wildcard po/*.po)
MOFILES=$(patsubst %.po,%.mo,$(POFILES))
LINGUAS=$(basename $(POFILES))
POTFILE=po/$(DOMAIN).pot

# dist is primarily for use when packaging; for development we still manage
# dependencies via `go get` explicitly.
# TODO: use git describe for versioning
VERSION=$(shell grep "var Version" shared/flex.go | sed -r -e 's/.*"([0-9\.]*)"/\1/')
ARCHIVE=lxd-$(VERSION).tar

.PHONY: default
default:
	go install -v ./...

.PHONY: check
check: default
	go test ./...
	cd test && ./main.sh

.PHONY: dist
dist:
	rm -Rf lxd-$(VERSION) $(ARCHIVE) $(ARCHIVE).gz
	mkdir -p lxd-$(VERSION)/dist
	GOPATH=$(shell pwd)/lxd-$(VERSION)/dist go get -d -v ./...
	rm -rf $(shell pwd)/lxd-$(VERSION)/dist/src/github.com/lxc/lxd
	ln -s ../../../.. ./lxd-$(VERSION)/dist/src/github.com/lxc/lxd
	git archive --prefix=lxd-$(VERSION)/ --output=$(ARCHIVE) HEAD
	tar -uf $(ARCHIVE) --exclude-vcs lxd-$(VERSION)/
	gzip -9 $(ARCHIVE)
	rm -Rf dist lxd-$(VERSION) $(ARCHIVE)

.PHONY: i18n update-po update-pot build-mo
i18n: update-pot

po/%.mo: po/%.po
	msgfmt --statistics -o $@ $<

po/%.po: po/$(DOMAIN).pot
	msgmerge -U po/$*.po po/$(DOMAIN).pot

update-po:
	-for lang in $(LINGUAS); do\
	    msgmerge -U $$lang.po po/$(DOMAIN).pot; \
	done

update-pot:
	xgettext -d $(DOMAIN) -s client.go lxc/*.go -o po/$(DOMAIN).pot -L c++ -i --keyword=Gettext

build-mo: $(MOFILES)
