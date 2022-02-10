DOMAIN=lxd
POFILES=$(wildcard po/*.po)
MOFILES=$(patsubst %.po,%.mo,$(POFILES))
LINGUAS=$(basename $(POFILES))
POTFILE=po/$(DOMAIN).pot
VERSION=$(shell grep "var Version" shared/version/flex.go | cut -d'"' -f2)
ARCHIVE=lxd-$(VERSION).tar
HASH := \#
TAG_SQLITE3=$(shell printf "$(HASH)include <dqlite.h>\nvoid main(){dqlite_node_id n = 1;}" | $(CC) ${CGO_CFLAGS} -o /dev/null -xc - >/dev/null 2>&1 && echo "libsqlite3")
GOPATH ?= $(shell go env GOPATH)
CGO_LDFLAGS_ALLOW ?= (-Wl,-wrap,pthread_create)|(-Wl,-z,now)
SPHINXENV=.sphinx/venv/bin/activate

ifneq "$(wildcard vendor)" ""
	RAFT_PATH=$(CURDIR)/vendor/raft
	DQLITE_PATH=$(CURDIR)/vendor/dqlite
else
	RAFT_PATH=$(GOPATH)/deps/raft
	DQLITE_PATH=$(GOPATH)/deps/dqlite
endif

	# raft
.PHONY: default
default: build

.PHONY: build
build:
ifeq "$(TAG_SQLITE3)" ""
	@echo "Missing dqlite, run \"make deps\" to setup."
	exit 1
endif

	CC="$(CC)" CGO_LDFLAGS_ALLOW="$(CGO_LDFLAGS_ALLOW)" go install -v -tags "$(TAG_SQLITE3)" $(DEBUG) ./...
	CGO_ENABLED=0 go install -v -tags netgo ./lxd-migrate
	CGO_ENABLED=0 go install -v -tags agent,netgo ./lxd-agent
	@echo "LXD built successfully"

.PHONY: client
client:
	go install -v -tags "$(TAG_SQLITE3)" $(DEBUG) ./lxc
	@echo "LXD client built successfully"

.PHONY: lxd-agent
lxd-agent:
	CGO_ENABLED=0 go install -v -tags agent,netgo ./lxd-agent
	@echo "LXD agent built successfully"

.PHONY: lxd-migrate
lxd-migrate:
	CGO_ENABLED=0 go install -v -tags netgo ./lxd-migrate
	@echo "LXD-MIGRATE built successfully"

.PHONY: deps
deps:
	@if [ ! -e "$(RAFT_PATH)" ]; then \
		git clone --depth=1 "https://github.com/canonical/raft" "$(RAFT_PATH)"; \
	elif [ -e "$(RAFT_PATH)/.git" ]; then \
		cd "$(RAFT_PATH)"; git pull; \
	fi

	cd "$(RAFT_PATH)" && \
		autoreconf -i && \
		./configure && \
		make

	# dqlite
	@if [ ! -e "$(DQLITE_PATH)" ]; then \
		git clone --depth=1 "https://github.com/canonical/dqlite" "$(DQLITE_PATH)"; \
	elif [ -e "$(DQLITE_PATH)/.git" ]; then \
		cd "$(DQLITE_PATH)"; git pull; \
	fi

	cd "$(DQLITE_PATH)" && \
		autoreconf -i && \
		PKG_CONFIG_PATH="$(RAFT_PATH)" ./configure && \
		make CFLAGS="-I$(RAFT_PATH)/include/" LDFLAGS="-L$(RAFT_PATH)/.libs/"

	# environment
	@echo ""
	@echo "Please set the following in your environment (possibly ~/.bashrc)"
	@echo "export CGO_CFLAGS=\"-I$(RAFT_PATH)/include/ -I$(DQLITE_PATH)/include/\""
	@echo "export CGO_LDFLAGS=\"-L$(RAFT_PATH)/.libs -L$(DQLITE_PATH)/.libs/\""
	@echo "export LD_LIBRARY_PATH=\"$(RAFT_PATH)/.libs/:$(DQLITE_PATH)/.libs/\""
	@echo "export CGO_LDFLAGS_ALLOW=\"(-Wl,-wrap,pthread_create)|(-Wl,-z,now)\""

.PHONY: update
update:
ifneq "$(LXD_OFFLINE)" ""
	@echo "The update target cannot be run in offline mode."
	exit 1
endif
	go get -t -v -d -u ./...
	go mod tidy
	@echo "Dependencies updated"

.PHONY: update-protobuf
update-protobuf:
	protoc --go_out=. ./lxd/migration/migrate.proto

.PHONY: update-schema
update-schema:
	cd lxd/db/generate && go build -o $(GOPATH)/bin/lxd-generate -tags "$(TAG_SQLITE3)" $(DEBUG) && cd -
	go generate ./...
	gofmt -s -w ./lxd/db/
	goimports -w ./lxd/db/
	@echo "Code generation completed"

.PHONY: update-api
update-api:
ifeq "$(LXD_OFFLINE)" ""
	(cd / ; GO111MODULE=on go get -v -x github.com/go-swagger/go-swagger/cmd/swagger)
endif
	swagger generate spec -o doc/rest-api.yaml -w ./lxd -m

.PHONY: doc
doc:
	@echo "Setting up documentation build environment"
	python3 -m venv .sphinx/venv
	. $(SPHINXENV) ; pip install --upgrade -r .sphinx/requirements.txt
	mkdir -p .sphinx/deps/ .sphinx/themes/
	git -C .sphinx/deps/swagger-ui pull || git clone --depth 1 https://github.com/swagger-api/swagger-ui.git .sphinx/deps/swagger-ui
	mkdir -p .sphinx/_static/swagger-ui
	ln -sf ../../deps/swagger-ui/dist/swagger-ui-bundle.js ../../deps/swagger-ui/dist/swagger-ui-standalone-preset.js ../../deps/swagger-ui/dist/swagger-ui.css .sphinx/_static/swagger-ui/
	wget -N -P .sphinx/_static/download https://linuxcontainers.org/static/img/favicon.ico https://linuxcontainers.org/static/img/containers.png https://linuxcontainers.org/static/img/containers.small.png
	rm -Rf doc/html
	make doc-incremental

.PHONY: doc-incremental
doc-incremental:
	@echo "Build the documentation"
	. $(SPHINXENV) ; sphinx-build -c .sphinx/ -b dirhtml doc/ doc/html/ -w .sphinx/warnings.txt

.PHONY: doc-serve
doc-serve:
	cd doc/html; python3 -m http.server 8001

.PHONY: debug
debug:
ifeq "$(TAG_SQLITE3)" ""
	@echo "Missing custom libsqlite3, run \"make deps\" to setup."
	exit 1
endif

	CC="$(CC)" CGO_LDFLAGS_ALLOW="$(CGO_LDFLAGS_ALLOW)" go install -v -tags "$(TAG_SQLITE3) logdebug" $(DEBUG) ./...
	CGO_ENABLED=0 go install -v -tags "netgo,logdebug" ./lxd-migrate
	CGO_ENABLED=0 go install -v -tags "agent,netgo,logdebug" ./lxd-agent
	@echo "LXD built successfully"

.PHONY: nocache
nocache:
ifeq "$(TAG_SQLITE3)" ""
	@echo "Missing custom libsqlite3, run \"make deps\" to setup."
	exit 1
endif

	CC="$(CC)" CGO_LDFLAGS_ALLOW="$(CGO_LDFLAGS_ALLOW)" go install -a -v -tags "$(TAG_SQLITE3)" $(DEBUG) ./...
	CGO_ENABLED=0 go install -a -v -tags netgo ./lxd-migrate
	CGO_ENABLED=0 go install -a -v -tags agent,netgo ./lxd-agent
	@echo "LXD built successfully"

race:
ifeq "$(TAG_SQLITE3)" ""
	@echo "Missing custom libsqlite3, run \"make deps\" to setup."
	exit 1
endif

	CC="$(CC)" CGO_LDFLAGS_ALLOW="$(CGO_LDFLAGS_ALLOW)" go install -race -v -tags "$(TAG_SQLITE3)" $(DEBUG) ./...
	CGO_ENABLED=0 go install -v -tags netgo ./lxd-migrate
	CGO_ENABLED=0 go install -v -tags agent,netgo ./lxd-agent
	@echo "LXD built successfully"

.PHONY: check
check: default
ifeq "$(LXD_OFFLINE)" ""
	(cd / ; go get -v -x github.com/rogpeppe/godeps)
	(cd / ; go get -v -x github.com/tsenart/deadcode)
	(cd / ; go get -v -x golang.org/x/lint/golint)
endif
	CGO_LDFLAGS_ALLOW="$(CGO_LDFLAGS_ALLOW)" go test -v -tags "$(TAG_SQLITE3)" $(DEBUG) ./...
	cd test && ./main.sh

.PHONY: dist
dist: doc
	# Cleanup
	rm -Rf $(ARCHIVE).gz

	# Create build dir
	$(eval TMP := $(shell mktemp -d))
	git archive --prefix=lxd-$(VERSION)/ HEAD | tar -x -C $(TMP)
	git show-ref HEAD | cut -d' ' -f1 > $(TMP)/lxd-$(VERSION)/.gitref

	# Download dependencies
	(cd $(TMP)/lxd-$(VERSION) ; go mod vendor)

	# Download the dqlite libraries
	git clone --depth=1 https://github.com/canonical/dqlite $(TMP)/lxd-$(VERSION)/vendor/dqlite
	(cd $(TMP)/lxd-$(VERSION)/vendor/dqlite ; git show-ref HEAD | cut -d' ' -f1 > .gitref)

	git clone --depth=1 https://github.com/canonical/raft $(TMP)/lxd-$(VERSION)/vendor/raft
	(cd $(TMP)/lxd-$(VERSION)/vendor/raft ; git show-ref HEAD | cut -d' ' -f1 > .gitref)

	# Copy doc output
	cp -r doc/html $(TMP)/lxd-$(VERSION)/doc/html/

	# Assemble tarball
	tar --exclude-vcs -C $(TMP) -zcf $(ARCHIVE).gz lxd-$(VERSION)/

	# Cleanup
	rm -Rf $(TMP)

.PHONY: i18n
i18n: update-pot update-po

po/%.mo: po/%.po
	msgfmt --statistics -o $@ $<

po/%.po: po/$(DOMAIN).pot
	msgmerge -U po/$*.po po/$(DOMAIN).pot

.PHONY: update-po
update-po:
	for lang in $(LINGUAS); do\
	    msgmerge -U $$lang.po po/$(DOMAIN).pot; \
	    rm -f $$lang.po~; \
	done

.PHONY: update-pot
update-pot:
ifeq "$(LXD_OFFLINE)" ""
	(cd / ; go get -v -x github.com/snapcore/snapd/i18n/xgettext-go)
endif
	xgettext-go -o po/$(DOMAIN).pot --add-comments-tag=TRANSLATORS: --sort-output --package-name=$(DOMAIN) --msgid-bugs-address=lxc-devel@lists.linuxcontainers.org --keyword=i18n.G --keyword-plural=i18n.NG lxc/*.go lxc/*/*.go

.PHONY: build-mo
build-mo: $(MOFILES)

.PHONY: static-analysis
static-analysis:
	(cd test; sh -c ". suites/static_analysis.sh; test_static_analysis")

.PHONY: tags
tags: *.go lxd/*.go shared/*.go lxc/*.go
	find . | grep \.go | grep -v git | grep -v .swp | grep -v vagrant | xargs gotags > tags
