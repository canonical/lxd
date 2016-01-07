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
	# Must run twice due to go get race
	-go get -t -v -d ./...
	-go get -t -v -d ./...
	go install -v ./...
	@echo "LXD built succesfuly"

.PHONY: client
client:
	# Must run twice due to go get race
	-go get -t -v -d ./...
	-go get -t -v -d ./...
	go install -v ./lxc
	@echo "LXD client built succesfuly"

.PHONY: update
update:
	# Must run twice due to go get race
	-go get -t -v -d -u ./...
	go get -t -v -d -u ./...
	@echo "Dependencies updated"

# This only needs to be done when migrate.proto is actually changed; since we
# commit the .pb.go in the tree and it's not expected to change very often,
# it's not a default build step.
.PHONY: protobuf
protobuf:
	protoc --go_out=. ./lxd/migrate.proto

.PHONY: check
check: default
	go get -v -x github.com/remyoudompheng/go-misc/deadcode
	@# go vet can (and does on go < 1.5) fail
	go get -v -x golang.org/x/tools/cmd/vet || true
	go test -v ./...
	cd test && ./main.sh

gccgo:
	go build -compiler gccgo ./...
	@echo "LXD built succesfuly with gccgo"

.PHONY: dist
dist:
	rm -Rf lxd-$(VERSION) $(ARCHIVE) $(ARCHIVE).gz
	mkdir -p lxd-$(VERSION)/dist
	-GOPATH=$(shell pwd)/lxd-$(VERSION)/dist go get -t -v -d ./...
	GOPATH=$(shell pwd)/lxd-$(VERSION)/dist go get -t -v -d ./...
	rm -rf $(shell pwd)/lxd-$(VERSION)/dist/src/github.com/lxc/lxd
	ln -s ../../../.. ./lxd-$(VERSION)/dist/src/github.com/lxc/lxd
	git archive --prefix=lxd-$(VERSION)/ --output=$(ARCHIVE) HEAD
	tar -uf $(ARCHIVE) --exclude-vcs lxd-$(VERSION)/
	gzip -9 $(ARCHIVE)
	rm -Rf dist lxd-$(VERSION) $(ARCHIVE)

.PHONY: i18n update-po update-pot build-mo static-analysis
i18n: update-pot

po/%.mo: po/%.po
	msgfmt --statistics -o $@ $<

po/%.po: po/$(DOMAIN).pot
	msgmerge -U po/$*.po po/$(DOMAIN).pot

update-po:
	-for lang in $(LINGUAS); do\
	    msgmerge -U $$lang.po po/$(DOMAIN).pot; \
	    rm -f $$lang.po~; \
	done

update-pot:
	go get -v -x github.com/ubuntu-core/snappy/i18n/xgettext-go/
	xgettext-go -o po/$(DOMAIN).pot --add-comments-tag=TRANSLATORS: --sort-output --package-name=$(DOMAIN) --msgid-bugs-address=lxc-devel@lists.linuxcontainers.org --keyword=i18n.G --keyword-plural=i18n.NG *.go shared/*.go lxc/*.go lxd/*.go


build-mo: $(MOFILES)

static-analysis:
	/bin/bash -x -c ". test/static_analysis.sh; static_analysis"

tags: *.go lxd/*.go shared/*.go lxc/*.go
	find . | grep \.go | grep -v git | grep -v .swp | grep -v vagrant | xargs gotags > tags
