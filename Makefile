DOMAIN=lxd
POFILES=$(wildcard po/*.po)
MOFILES=$(patsubst %.po,%.mo,$(POFILES))
LINGUAS=$(basename $(POFILES))
POTFILE=po/$(DOMAIN).pot
VERSION=$(or ${CUSTOM_VERSION},$(shell grep "var Version" shared/version/flex.go | cut -d'"' -f2))
ARCHIVE=lxd-$(VERSION).tar
HASH := \#
TAG_SQLITE3=$(shell printf "$(HASH)include <dqlite.h>\nvoid main(){dqlite_node_id n = 1;}" | $(CC) ${CGO_CFLAGS} -o /dev/null -xc - >/dev/null 2>&1 && echo "libsqlite3")
GOPATH ?= $(shell go env GOPATH)
CGO_LDFLAGS_ALLOW ?= (-Wl,-wrap,pthread_create)|(-Wl,-z,now)
SPHINXENV=doc/.sphinx/venv/bin/activate
SPHINXPIPPATH=doc/.sphinx/venv/bin/pip
GOMIN=1.22.7
GOCOVERDIR ?= $(shell go env GOCOVERDIR)

ifneq "$(wildcard vendor)" ""
	DQLITE_PATH=$(CURDIR)/vendor/dqlite
else
	DQLITE_PATH=$(GOPATH)/deps/dqlite
endif

.PHONY: default
default: all

.PHONY: all
all: client lxd lxd-agent lxd-migrate

.PHONY: build
build: lxd
.PHONY: lxd
lxd: lxd-metadata
ifeq "$(TAG_SQLITE3)" ""
	@echo "Missing dqlite, run \"make deps\" to setup."
	exit 1
endif

ifeq "$(GOCOVERDIR)" ""
	CC="$(CC)" CGO_LDFLAGS_ALLOW="$(CGO_LDFLAGS_ALLOW)" go install -v -tags "$(TAG_SQLITE3)" -trimpath $(DEBUG) ./lxd ./lxc-to-lxd
	CGO_ENABLED=0 go install -v -tags netgo -trimpath $(DEBUG) ./lxd-user ./lxd-benchmark
else
	CC="$(CC)" CGO_LDFLAGS_ALLOW="$(CGO_LDFLAGS_ALLOW)" go install -v -tags "$(TAG_SQLITE3)" -trimpath -cover $(DEBUG) ./lxd ./lxc-to-lxd
	CGO_ENABLED=0 go install -v -tags netgo -trimpath -cover $(DEBUG) ./lxd-user ./lxd-benchmark
endif

	@echo "LXD built successfully"

.PHONY: client
client:
ifeq "$(GOCOVERDIR)" ""
	go install -v -trimpath $(DEBUG) ./lxc
else
	go install -v -trimpath -cover $(DEBUG) ./lxc
endif

	@echo "LXD client built successfully"

.PHONY: lxd-agent
lxd-agent:
ifeq "$(GOCOVERDIR)" ""
	CGO_ENABLED=0 go install -v -trimpath -tags agent,netgo ./lxd-agent
else
	CGO_ENABLED=0 go install -v -trimpath -cover -tags agent,netgo ./lxd-agent
endif

	@echo "LXD agent built successfully"

.PHONY: lxd-metadata
lxd-metadata:
ifeq "$(GOCOVERDIR)" ""
	CGO_ENABLED=0 go install -v -trimpath -tags lxd-metadata ./lxd/lxd-metadata
else
	CGO_ENABLED=0 go install -v -trimpath -cover -tags lxd-metadata ./lxd/lxd-metadata
endif

	@echo "LXD metadata built successfully"

.PHONY: lxd-migrate
lxd-migrate:
ifeq "$(GOCOVERDIR)" ""
	CGO_ENABLED=0 go install -v -trimpath -tags netgo ./lxd-migrate
else
	CGO_ENABLED=0 go install -v -trimpath -cover -tags netgo ./lxd-migrate
endif

	@echo "LXD-MIGRATE built successfully"

.PHONY: deps
deps:
	# dqlite (+raft)
	@if [ ! -e "$(DQLITE_PATH)" ]; then \
		git clone --depth=1 "https://github.com/canonical/dqlite" "$(DQLITE_PATH)"; \
	elif [ -e "$(DQLITE_PATH)/.git" ]; then \
		cd "$(DQLITE_PATH)"; git pull; \
	fi

	cd "$(DQLITE_PATH)" && \
		autoreconf -i && \
		./configure --enable-build-raft && \
		make

	# environment
	@echo ""
	@echo "Please set the following in your environment (possibly ~/.bashrc)"
	@echo "export CGO_CFLAGS=\"-I$(DQLITE_PATH)/include/\""
	@echo "export CGO_LDFLAGS=\"-L$(DQLITE_PATH)/.libs/\""
	@echo "export LD_LIBRARY_PATH=\"$(DQLITE_PATH)/.libs/\""
	@echo "export CGO_LDFLAGS_ALLOW=\"(-Wl,-wrap,pthread_create)|(-Wl,-z,now)\""

.PHONY: update-gomod
update-gomod:
ifneq "$(LXD_OFFLINE)" ""
	@echo "The update-gomod target cannot be run in offline mode."
	exit 1
endif
	# Update gomod dependencies
	go get -t -v -u ./...

	# Static pins
	go get github.com/gorilla/websocket@v1.5.1 # Due to riscv64 crashes in LP

	# Enforce minimum go version
	go get toolchain@none # Use the bundled toolchain that meets the minimum go version
	go mod tidy -go=$(GOMIN)

	@echo "Dependencies updated"

.PHONY: update-protobuf
update-protobuf:
	protoc --go_out=. ./lxd/migration/migrate.proto

.PHONY: update-schema
update-schema:
	cd lxd/db/generate && go build -v -trimpath -o $(GOPATH)/bin/lxd-generate -tags "$(TAG_SQLITE3)" $(DEBUG) && cd -
	go generate ./...
	gofmt -s -w ./lxd/db/
	goimports -w ./lxd/db/
	@echo "Code generation completed"

.PHONY: update-api
update-api:
ifeq "$(LXD_OFFLINE)" ""
	(cd / ; go install github.com/go-swagger/go-swagger/cmd/swagger@latest)
endif
	@# Generate spec and exclude package from dependency which causes a 'classifier: unknown swagger annotation "extendee"' error.
	@# For more details see: https://github.com/go-swagger/go-swagger/issues/2917.
	swagger generate spec -o doc/rest-api.yaml -w ./lxd -m -x github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2/options

.PHONY: update-metadata
update-metadata: build
	@echo "Generating golang documentation metadata"
	$(GOPATH)/bin/lxd-metadata . --json ./lxd/metadata/configuration.json --txt ./doc/metadata.txt --substitution-db ./doc/substitutions.yaml

.PHONY: doc
doc: doc-clean doc-install doc-html doc-objects

.PHONY: doc-incremental
doc-incremental: doc-html

doc-%:
	cd doc && $(MAKE) -f Makefile $*

.PHONY: debug
debug:
ifeq "$(TAG_SQLITE3)" ""
	@echo "Missing custom libsqlite3, run \"make deps\" to setup."
	exit 1
endif

	CC="$(CC)" CGO_LDFLAGS_ALLOW="$(CGO_LDFLAGS_ALLOW)" go install -v -tags "$(TAG_SQLITE3) logdebug" $(DEBUG) ./...
	CGO_ENABLED=0 go install -v -trimpath -tags "netgo,logdebug" ./lxd-migrate
	CGO_ENABLED=0 go install -v -trimpath -tags "agent,netgo,logdebug" ./lxd-agent
	@echo "LXD built successfully"

.PHONY: check
check: default check-unit
	cd test && ./main.sh

.PHONY: unit
check-unit:
ifeq "$(GOCOVERDIR)" ""
	CGO_LDFLAGS_ALLOW="$(CGO_LDFLAGS_ALLOW)" go test -v -failfast -tags "$(TAG_SQLITE3)" $(DEBUG) ./...
else
	CGO_LDFLAGS_ALLOW="$(CGO_LDFLAGS_ALLOW)" go test -v -failfast -tags "$(TAG_SQLITE3)" $(DEBUG) ./... -cover -test.gocoverdir="${GOCOVERDIR}"
endif

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

	# Download the dqlite library
	git clone --depth=1 https://github.com/canonical/dqlite $(TMP)/lxd-$(VERSION)/vendor/dqlite
	(cd $(TMP)/lxd-$(VERSION)/vendor/dqlite ; git show-ref HEAD | cut -d' ' -f1 > .gitref)

	# Copy doc output
	cp -r doc/_build $(TMP)/lxd-$(VERSION)/doc/html/

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
	set -eu; \
	for lang in $(LINGUAS); do\
	    msgmerge --backup=none -U $$lang.po po/$(DOMAIN).pot; \
	done; \
	if [ -t 0 ] && ! git diff --quiet -- po/*.po; then \
		read -rp "Would you like to commit i18n changes (Y/n)? " answer; \
			if [ "$${answer:-y}" = "y" ] || [ "$${answer:-y}" = "Y" ]; then \
				git commit -sm "i18n: Update translations." -- po/*.po; fi; \
	fi

.PHONY: update-pot
update-pot:
ifeq "$(LXD_OFFLINE)" ""
	(cd / ; go install github.com/snapcore/snapd/i18n/xgettext-go@2.57.1)
endif
	xgettext-go -o po/$(DOMAIN).pot --add-comments-tag=TRANSLATORS: --sort-output --package-name=$(DOMAIN) --msgid-bugs-address=lxd@lists.canonical.com --keyword=i18n.G --keyword-plural=i18n.NG lxc/*.go lxc/*/*.go
	if git diff --quiet --ignore-matching-lines='^\s*"POT-Creation-Date: .*\n"' -- po/*.pot; then git checkout -- po/*.pot; fi
	if [ -t 0 ] && ! git diff --quiet --ignore-matching-lines='^\s*"POT-Creation-Date: .*\n"' -- po/*.pot; then \
		read -rp "Would you like to commit i18n template changes (Y/n)? " answer; \
			if [ "$${answer:-y}" = "y" ] || [ "$${answer:-y}" = "Y" ]; then \
				git commit -sm "i18n: Update translation templates." -- po/*.pot; fi; \
	fi

.PHONY: build-mo
build-mo: $(MOFILES)

.PHONY: static-analysis
static-analysis:
ifeq ($(shell command -v go-licenses),)
	(cd / ; go install github.com/google/go-licenses@latest)
endif
ifeq ($(shell command -v golangci-lint),)
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $$(go env GOPATH)/bin
endif
ifeq ($(shell command -v shellcheck),)
	echo "Please install shellcheck"
	exit 1
else
ifneq "$(shell shellcheck --version | grep version: | cut -d ' ' -f2)" "0.8.0"
	@echo "WARN: shellcheck version is not 0.8.0"
endif
endif
ifeq ($(shell command -v flake8),)
	echo "Please install flake8"
	exit 1
endif
	flake8 test/deps/import-busybox
	shellcheck test/*.sh test/includes/*.sh test/suites/*.sh test/backends/*.sh test/lint/*.sh test/extras/*.sh
	NOT_EXEC="$(shell find test/lint -type f -not -executable)"; \
	if [ -n "$$NOT_EXEC" ]; then \
        echo "lint scripts not executable: $$NOT_EXEC"; \
        exit 1; \
	fi
	BAD_NAME="$(shell find test/lint -type f -not -name '*.sh')"; \
	if [ -n "$$BAD_NAME" ]; then \
        echo "lint scripts missing .sh extension: $$BAD_NAME"; \
        exit 1; \
	fi
	run-parts --verbose --exit-on-error --regex '.sh' test/lint

.PHONY: update-auth
update-auth:
	go generate ./lxd/auth
