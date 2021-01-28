DOMAIN=lxd
POFILES=$(wildcard po/*.po)
MOFILES=$(patsubst %.po,%.mo,$(POFILES))
LINGUAS=$(basename $(POFILES))
POTFILE=po/$(DOMAIN).pot
VERSION=$(shell grep "var Version" shared/version/flex.go | cut -d'"' -f2)
ARCHIVE=lxd-$(VERSION).tar
HASH := \#
TAG_SQLITE3=$(shell printf "$(HASH)include <dqlite.h>\nvoid main(){dqlite_node_id n = 1;}" | $(CC) ${CGO_CFLAGS} -o /dev/null -xc - >/dev/null 2>&1 && echo "libsqlite3")
GOPATH ?= $(HOME)/go

.PHONY: default
default:
ifeq ($(TAG_SQLITE3),)
	@echo "Missing dqlite, run \"make deps\" to setup."
	exit 1
endif

	go get -t -v -d ./...
	CC=$(CC) go install -v -tags "$(TAG_SQLITE3)" $(DEBUG) ./...
	CGO_ENABLED=0 go install -v -tags netgo ./lxd-p2c
	CGO_ENABLED=0 go install -v -tags agent,netgo ./lxd-agent
	@echo "LXD built successfully"

.PHONY: client
client:
	go get -t -v -d ./...
	go install -v -tags "$(TAG_SQLITE3)" $(DEBUG) ./lxc
	@echo "LXD client built successfully"

.PHONY: lxd-agent
lxd-agent:
	CGO_ENABLED=0 go install -v -tags agent,netgo ./lxd-agent
	@echo "LXD agent built successfully"

.PHONY: lxd-p2c
lxd-p2c:
	CGO_ENABLED=0 go install -v -tags netgo ./lxd-p2c
	@echo "LXD-P2C built successfully"

.PHONY: deps
deps:
	# raft
	@if [ -d "$(GOPATH)/deps/raft" ]; then \
		if [ -d "$(GOPATH)/deps/raft/.git" ]; then \
			cd "$(GOPATH)/deps/raft"; \
			git pull; \
		fi; \
	else \
		git clone --depth=1 "https://github.com/canonical/raft" "$(GOPATH)/deps/raft"; \
	fi

	cd "$(GOPATH)/deps/raft" && \
		autoreconf -i && \
		./configure && \
		make

	# dqlite
	@if [ -d "$(GOPATH)/deps/dqlite" ]; then \
		if [ -d "$(GOPATH)/deps/dqlite/.git" ]; then \
			cd "$(GOPATH)/deps/dqlite"; \
			git pull; \
		fi; \
	else \
		git clone --depth=1 "https://github.com/canonical/dqlite" "$(GOPATH)/deps/dqlite"; \
	fi

	cd "$(GOPATH)/deps/dqlite" && \
		autoreconf -i && \
		PKG_CONFIG_PATH="$(GOPATH)/deps/raft/" ./configure && \
		make CFLAGS="-I$(GOPATH)/deps/raft/include/" LDFLAGS="-L$(GOPATH)/deps/raft/.libs/"

	# environment
	@echo ""
	@echo "Please set the following in your environment (possibly ~/.bashrc)"
	@echo "export CGO_CFLAGS=\"-I$(GOPATH)/deps/raft/include/ -I$(GOPATH)/deps/dqlite/include/\""
	@echo "export CGO_LDFLAGS=\"-L$(GOPATH)/deps/raft/.libs -L$(GOPATH)/deps/dqlite/.libs/\""
	@echo "export LD_LIBRARY_PATH=\"$(GOPATH)/deps/raft/.libs/:$(GOPATH)/deps/dqlite/.libs/\""
	@echo "export CGO_LDFLAGS_ALLOW=\"-Wl,-wrap,pthread_create\""


.PHONY: update
update:
	go get -t -v -d -u ./...
	@echo "Dependencies updated"

.PHONY: update-protobuf
update-protobuf:
	protoc --go_out=. ./lxd/migration/migrate.proto

.PHONY: update-schema
update-schema:
	cd lxd/db/generate && go build -o lxd-generate -tags "$(TAG_SQLITE3)" $(DEBUG) && cd -
	mv lxd/db/generate/lxd-generate $(GOPATH)/bin
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
	CGO_ENABLED=0 go install -v -tags "netgo,logdebug" ./lxd-p2c
	CGO_ENABLED=0 go install -v -tags "agent,netgo,logdebug" ./lxd-agent
	@echo "LXD built successfully"

.PHONY: nocache
nocache:
ifeq ($(TAG_SQLITE3),)
	@echo "Missing custom libsqlite3, run \"make deps\" to setup."
	exit 1
endif

	go get -t -v -d ./...
	CC=$(CC) go install -a -v -tags "$(TAG_SQLITE3)" $(DEBUG) ./...
	CGO_ENABLED=0 go install -a -v -tags netgo ./lxd-p2c
	CGO_ENABLED=0 go install -a -v -tags agent,netgo ./lxd-agent
	@echo "LXD built successfully"

race:
ifeq ($(TAG_SQLITE3),)
	@echo "Missing custom libsqlite3, run \"make deps\" to setup."
	exit 1
endif

	go get -t -v -d ./...
	CC=$(CC) go install -race -v -tags "$(TAG_SQLITE3)" $(DEBUG) ./...
	CGO_ENABLED=0 go install -v -tags netgo ./lxd-p2c
	CGO_ENABLED=0 go install -v -tags agent,netgo ./lxd-agent
	@echo "LXD built successfully"

.PHONY: check
check: default
	go get -v -x github.com/rogpeppe/godeps
	go get -v -x github.com/tsenart/deadcode
	go get -v -x golang.org/x/lint/golint
	go test -v -tags "$(TAG_SQLITE3)" $(DEBUG) ./...
	cd test && ./main.sh

.PHONY: dist
dist:
	# Cleanup
	rm -Rf $(ARCHIVE).gz

	# Create build dir
	$(eval TMP := $(shell mktemp -d))
	git archive --prefix=lxd-$(VERSION)/ HEAD | tar -x -C $(TMP)
	mkdir -p $(TMP)/_dist/src/github.com/lxc
	ln -s ../../../../lxd-$(VERSION) $(TMP)/_dist/src/github.com/lxc/lxd

	# Download dependencies
	cd $(TMP)/lxd-$(VERSION) && GOPATH=$(TMP)/_dist go get -t -v -d ./...

	# Download the cluster-enabled sqlite/dqlite
	mkdir $(TMP)/_dist/deps/
	git clone --depth=1 https://github.com/canonical/dqlite $(TMP)/_dist/deps/dqlite
	git clone --depth=1 https://github.com/canonical/raft $(TMP)/_dist/deps/raft

	# Write a manifest
	cd $(TMP)/_dist && find . -type d -name .git | while read line; do GITDIR=$$(dirname $$line); echo "$${GITDIR}: $$(cd $${GITDIR} && git show-ref HEAD $${GITDIR} | cut -d' ' -f1)"; done | sort > $(TMP)/_dist/MANIFEST

	# Assemble tarball
	rm $(TMP)/_dist/src/github.com/lxc/lxd
	ln -s ../../../../ $(TMP)/_dist/src/github.com/lxc/lxd
	mv $(TMP)/_dist $(TMP)/lxd-$(VERSION)/
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
