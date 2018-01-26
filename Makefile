DOMAIN=lxd
POFILES=$(wildcard po/*.po)
MOFILES=$(patsubst %.po,%.mo,$(POFILES))
LINGUAS=$(basename $(POFILES))
POTFILE=po/$(DOMAIN).pot
GO_SERVER=./lxd/.go-wrapper

# dist is primarily for use when packaging; for development we still manage
# dependencies via `go get` explicitly.
# TODO: use git describe for versioning
VERSION=$(shell grep "var Version" shared/version/flex.go | cut -d'"' -f2)
ARCHIVE=lxd-$(VERSION).tar
TAGS=$(shell printf "\#include <sqlite3.h>\nvoid main(){}" | $(CC) -o /dev/null -xc - >/dev/null 2>&1 && echo "-tags libsqlite3")

.PHONY: default
default:
	$(GO_SERVER) get -t -v -d ./...
	$(GO_SERVER) install -v $(TAGS) $(DEBUG) ./...
	@echo "LXD built successfully"

.PHONY: client
client:
	go get -t -v -d ./...
	go install -v $(TAGS) $(DEBUG) ./lxc
	@echo "LXD client built successfully"

.PHONY: update
update:
	go get -t -v -d -u ./...
	@echo "Dependencies updated"

.PHONY: update
update-schema:
	go run -v $(TAGS) ./lxd/schema.go
	@echo "Schema source code updated"

.PHONY: debug
debug:
	go get -t -v -d ./...
	go install -v $(TAGS) -tags logdebug $(DEBUG) ./...
	@echo "LXD built successfully"

# This only needs to be done when migrate.proto is actually changed; since we
# commit the .pb.go in the tree and it's not expected to change very often,
# it's not a default build step.
.PHONY: protobuf
protobuf:
	protoc --go_out=. ./lxd/migrate.proto

.PHONY: check
check: default
	go get -v -x github.com/rogpeppe/godeps
	go get -v -x github.com/remyoudompheng/go-misc/deadcode
	go get -v -x github.com/golang/lint/golint
	go test -v $(TAGS) $(DEBUG) ./...
	cd test && ./main.sh

gccgo:
	go build -v $(TAGS) $(DEBUG) -compiler gccgo ./...
	@echo "LXD built successfully with gccgo"

.PHONY: dist
dist:
	# Cleanup
	rm -Rf $(ARCHIVE).gz

	# Create build dir
	$(eval TMP := $(shell mktemp -d))
	git archive --prefix=lxd-$(VERSION)/ HEAD | tar -x -C $(TMP)
	mkdir -p $(TMP)/dist/src/github.com/lxc
	ln -s ../../../../lxd-$(VERSION) $(TMP)/dist/src/github.com/lxc/lxd

	# Download dependencies
	cd $(TMP)/lxd-$(VERSION) && GOPATH=$(TMP)/dist go get -t -v -d ./...

	# Workaround for gorilla/mux on Go < 1.7
	cd $(TMP)/lxd-$(VERSION) && GOPATH=$(TMP)/dist go get -v -d github.com/gorilla/context

	# Assemble tarball
	rm $(TMP)/dist/src/github.com/lxc/lxd
	ln -s ../../../../ $(TMP)/dist/src/github.com/lxc/lxd
	mv $(TMP)/dist $(TMP)/lxd-$(VERSION)/
	tar --exclude-vcs -C $(TMP) -zcf $(ARCHIVE).gz lxd-$(VERSION)/

	# Cleanup
	rm -Rf $(TMP)

.PHONY: i18n update-po update-pot build-mo static-analysis
i18n: update-pot update-po

po/%.mo: po/%.po
	msgfmt --statistics -o $@ $<

po/%.po: po/$(DOMAIN).pot
	msgmerge -U po/$*.po po/$(DOMAIN).pot

update-po:
	for lang in $(LINGUAS); do\
	    msgmerge -U $$lang.po po/$(DOMAIN).pot; \
	    rm -f $$lang.po~; \
	done

update-pot:
	go get -v -x github.com/snapcore/snapd/i18n/xgettext-go/
	xgettext-go -o po/$(DOMAIN).pot --add-comments-tag=TRANSLATORS: --sort-output --package-name=$(DOMAIN) --msgid-bugs-address=lxc-devel@lists.linuxcontainers.org --keyword=i18n.G --keyword-plural=i18n.NG shared/*.go lxc/*.go lxd/*.go

build-mo: $(MOFILES)

.PHONY: build-sqlite
build-sqlite:
	cd lxd/sqlite && \
	    git log -1 --format=format:%ci%n | sed -e 's/ [-+].*//;s/ /T/;s/^/D /' > manifest && \
	    echo $(shell git log -1 --format=format:%H) > manifest.uuid && \
	    ./configure && \
	    make

static-analysis:
	(cd test;  /bin/sh -x -c ". suites/static_analysis.sh; test_static_analysis")

tags: *.go lxd/*.go shared/*.go lxc/*.go
	find . | grep \.go | grep -v git | grep -v .swp | grep -v vagrant | xargs gotags > tags
