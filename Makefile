.PHONY: default
default:
	go install -v ./...

.PHONY: check
check: default
	go fmt ./...
	go vet ./...
	git diff --exit-code
	cd test && ./main.sh

# dist is primarily for use when packaging; for development we still manage
# dependencies via `go get` explicitly.
# TODO: use git describe for versioning
VERSION=$(shell grep "var Version" flex.go | sed -r -e 's/.*"([0-9\.]*)"/\1/')
ARCHIVE=lxd-$(VERSION).tar

.PHONY: dist
dist:
	mkdir -p dist
	GOPATH=$(shell pwd)/dist go get -d -v ./...
	rm -rf $(shell pwd)/dist/src/github.com/lxc/lxd
	ln -s ../../../.. ./dist/src/github.com/lxc/lxd
	git archive --output=../$(ARCHIVE) HEAD
	tar -uf ../$(ARCHIVE) --exclude-vcs ./dist
	gzip ../$(ARCHIVE)

.PHONY: i18n
i18n:
	xgettext -d lxd -s client.go lxc/*.go -o po/lxd.pot -L c++ -i --keyword=Gettext
