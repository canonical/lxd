DOMAIN=lxd
POFILES=$(wildcard po/*.po)
MOFILES=$(patsubst %.po,%.mo,$(POFILES))
LINGUAS=$(basename $(POFILES))
POTFILE=po/$(DOMAIN).pot

# dist is primarily for use when packaging; for development we still manage
# dependencies via `go get` explicitly.
# TODO: use git describe for versioning
VERSION=$(shell grep "var Version" shared/version/flex.go | cut -d'"' -f2)
ARCHIVE=lxd-$(VERSION).tar

.PHONY: default
default:
	# Must a few times due to go get race
	-go get -t -v -d ./...
	-go get -t -v -d ./...
	-go get -t -v -d ./...
	go install -v $(DEBUG) ./...
	@echo "LXD built successfully"

.PHONY: client
client:
	# Must a few times due to go get race
	-go get -t -v -d ./...
	-go get -t -v -d ./...
	-go get -t -v -d ./...
	go install -v $(DEBUG) ./lxc
	@echo "LXD client built successfully"

.PHONY: update
update:
	# Must a few times due to go get race
	-go get -t -v -d -u ./...
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
	go get -v -x github.com/rogpeppe/godeps
	go get -v -x github.com/remyoudompheng/go-misc/deadcode
	go test -v ./...
	cd test && ./main.sh

gccgo:
	go build -compiler gccgo ./...
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
	-cd $(TMP)/lxd-$(VERSION) && GOPATH=$(TMP)/dist go get -t -v -d ./...
	-cd $(TMP)/lxd-$(VERSION) && GOPATH=$(TMP)/dist go get -t -v -d ./...
	-cd $(TMP)/lxd-$(VERSION) && GOPATH=$(TMP)/dist go get -t -v -d ./...
	cd $(TMP)/lxd-$(VERSION) && GOPATH=$(TMP)/dist go get -t -v -d ./...
	
	# Assemble tarball
	rm $(TMP)/dist/src/github.com/lxc/lxd
	ln -s ../../../../ $(TMP)/dist/src/github.com/lxc/lxd
	mv $(TMP)/dist $(TMP)/lxd-$(VERSION)/
	tar --exclude-vcs -C $(TMP) -zcf $(ARCHIVE).gz lxd-$(VERSION)/
	
	# Cleanup
	rm -Rf $(TMP)

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
	go get -v -x github.com/snapcore/snapd/i18n/xgettext-go/
	xgettext-go -o po/$(DOMAIN).pot --add-comments-tag=TRANSLATORS: --sort-output --package-name=$(DOMAIN) --msgid-bugs-address=lxc-devel@lists.linuxcontainers.org --keyword=i18n.G --keyword-plural=i18n.NG *.go shared/*.go lxc/*.go lxd/*.go


build-mo: $(MOFILES)

static-analysis:
	/bin/bash -x -c ". test/static_analysis.sh; static_analysis"

tags: *.go lxd/*.go shared/*.go lxc/*.go
	find . | grep \.go | grep -v git | grep -v .swp | grep -v vagrant | xargs gotags > tags
