DOMAIN=lxd
POFILES=$(wildcard po/*.po)
MOFILES=$(patsubst %.po,%.mo,$(POFILES))
LINGUAS=$(basename $(POFILES))
POTFILE=po/$(DOMAIN).pot
VERSION=$(shell grep "var Version" shared/version/flex.go | cut -d'"' -f2)
ARCHIVE=lxd-$(VERSION).tar
TAG_SQLITE3=$(shell printf "\#include <sqlite3.h>\nvoid main(){int n = SQLITE_IOERR_NOT_LEADER;}" | $(CC) ${CGO_CFLAGS} -o /dev/null -xc - >/dev/null 2>&1 && echo "libsqlite3")
GOPATH ?= $(HOME)/go

.PHONY: default
default:
ifeq ($(TAG_SQLITE3),)
	@echo "Missing custom libsqlite3, run \"make deps\" to setup."
	exit 1
endif

	go get -t -v -d ./...
	CC=$(CC) go install -v -tags "$(TAG_SQLITE3)" $(DEBUG) ./...
	@echo "LXD built successfully"

.PHONY: client
client:
	go get -t -v -d ./...
	go install -v -tags "$(TAG_SQLITE3)" $(DEBUG) ./lxc
	@echo "LXD client built successfully"

.PHONY: lxd-p2c
lxd-p2c:
	CGO_ENABLED=0 go install -v -tags netgo ./lxd-p2c
	@echo "LXD-P2C built successfully"

.PHONY: deps
deps:
	# sqlite
	@if [ -d "$(GOPATH)/deps/sqlite" ]; then \
		cd "$(GOPATH)/deps/sqlite"; \
		git pull; \
	else \
		git clone --depth=1 "https://github.com/CanonicalLtd/sqlite" "$(GOPATH)/deps/sqlite"; \
	fi

	cd "$(GOPATH)/deps/sqlite" && \
		./configure --enable-replication --disable-amalgamation --disable-tcl && \
		git log -1 --format="format:%ci%n" | sed -e 's/ [-+].*$$//;s/ /T/;s/^/D /' > manifest && \
		git log -1 --format="format:%H" > manifest.uuid && \
		make

	# dqlite
	@if [ -d "$(GOPATH)/deps/dqlite" ]; then \
		cd "$(GOPATH)/deps/dqlite"; \
		git pull; \
	else \
		git clone --depth=1 "https://github.com/CanonicalLtd/dqlite" "$(GOPATH)/deps/dqlite"; \
	fi

	cd "$(GOPATH)/deps/dqlite" && \
		autoreconf -i && \
		PKG_CONFIG_PATH="$(GOPATH)/deps/sqlite/" ./configure && \
		make CFLAGS="-I$(GOPATH)/deps/sqlite/" LDFLAGS="-L$(GOPATH)/deps/sqlite/.libs/"

	# environment
	@echo ""
	@echo "Please set the following in your environment (possibly ~/.bashrc)"
	@echo "export CGO_CFLAGS=\"-I$(GOPATH)/deps/sqlite/ -I$(GOPATH)/deps/dqlite/include/\""
	@echo "export CGO_LDFLAGS=\"-L$(GOPATH)/deps/sqlite/.libs/ -L$(GOPATH)/deps/dqlite/.libs/\""
	@echo "export LD_LIBRARY_PATH=\"$(GOPATH)/deps/sqlite/.libs/:$(GOPATH)/deps/dqlite/.libs/\""

.PHONY: update
update:
	go get -t -v -d -u ./...
	@echo "Dependencies updated"

.PHONY: update-protobuf
update-protobuf:
	protoc --go_out=. ./lxd/migration/migrate.proto

.PHONY: update-schema
generate:
	cd shared/generate && go build -o lxd-generate -tags "$(TAG_SQLITE3)" $(DEBUG) && cd -
	mv shared/generate/lxd-generate $(GOPATH)/bin
	go generate ./...
	@echo "Code generation completed"

.PHONY: debug
debug:
ifeq ($(TAG_SQLITE3),)
	@echo "Missing custom libsqlite3, run \"make deps\" to setup."
	exit 1
endif

	go get -t -v -d ./...
	CC=$(CC) go install -v -tags "$(TAG_SQLITE3) logdebug" $(DEBUG) ./...
	@echo "LXD built successfully"

.PHONY: check
check: default
	go get -v -x github.com/rogpeppe/godeps
	go get -v -x github.com/remyoudompheng/go-misc/deadcode
	go get -v -x github.com/golang/lint/golint
	go test -v -tags "$(TAG_SQLITE3)" $(DEBUG) ./...
	cd test && ./main.sh

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

	# Download the cluster-enabled sqlite/dqlite
	git clone --depth=1 https://github.com/CanonicalLtd/dqlite $(TMP)/dist/dqlite
	git clone --depth=1 https://github.com/CanonicalLtd/sqlite $(TMP)/dist/sqlite
	cd $(TMP)/dist/sqlite && git log -1 --format="format:%ci%n" | sed -e 's/ [-+].*$$//;s/ /T/;s/^/D /' > manifest
	cd $(TMP)/dist/sqlite && git log -1 --format="format:%H" > manifest.uuid

	# Write a manifest
	cd $(TMP)/dist && find . -type d -name .git | while read line; do GITDIR=$$(dirname $$line); echo "$${GITDIR}: $$(cd $${GITDIR} && git show-ref HEAD $${GITDIR} | cut -d' ' -f1)"; done | sort > $(TMP)/dist/MANIFEST

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
	xgettext-go -o po/$(DOMAIN).pot --add-comments-tag=TRANSLATORS: --sort-output --package-name=$(DOMAIN) --msgid-bugs-address=lxc-devel@lists.linuxcontainers.org --keyword=i18n.G --keyword-plural=i18n.NG lxc/*.go lxc/*/*.go

build-mo: $(MOFILES)

static-analysis:
	(cd test;  /bin/sh -x -c ". suites/static_analysis.sh; test_static_analysis")

tags: *.go lxd/*.go shared/*.go lxc/*.go
	find . | grep \.go | grep -v git | grep -v .swp | grep -v vagrant | xargs gotags > tags
