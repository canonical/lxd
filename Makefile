.PHONY: default
default:
	go install -v ./...

.PHONY: check
check: default
	go test ./...
	cd test && ./main.sh

# dist is primarily for use when packaging; for development we still manage
# dependencies via `go get` explicitly.
# TODO: use git describe for versioning
VERSION=$(shell grep "var Version" shared/flex.go | sed -r -e 's/.*"([0-9\.]*)"/\1/')
ARCHIVE=lxd-$(VERSION).tar

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

.PHONY: i18n
i18n:
	xgettext -d lxd -s client.go lxc/*.go -o po/lxd.pot -L c++ -i --keyword=Gettext
