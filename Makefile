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
GOMIN=1.23.7
GOTOOLCHAIN=local
export GOTOOLCHAIN
GOCOVERDIR ?= $(shell go env GOCOVERDIR)
DQLITE_BRANCH=lts-1.17.x

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
lxd:
ifeq "$(TAG_SQLITE3)" ""
	@echo "Missing dqlite, run \"make deps\" to setup."
	exit 1
endif

	CC="$(CC)" CGO_LDFLAGS_ALLOW="$(CGO_LDFLAGS_ALLOW)" go install -v -tags "$(TAG_SQLITE3)" $(DEBUG) ./lxd ./lxc-to-lxd
	CGO_ENABLED=0 go install -v -tags netgo ./lxd-migrate ./lxd-user ./lxd-benchmark

	@echo "LXD built successfully"

.PHONY: client
client:
	go install -v $(DEBUG) ./lxc
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
	# dqlite (+raft)
	@if [ ! -e "$(DQLITE_PATH)" ]; then \
		echo "Retrieving dqlite from ${DQLITE_BRANCH} branch"; \
		git clone --depth=1 --branch "${DQLITE_BRANCH}" "https://github.com/canonical/dqlite" "$(DQLITE_PATH)"; \
	elif [ -e "$(DQLITE_PATH)/.git" ]; then \
		echo "Updating existing dqlite branch"; \
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
	go get github.com/olekukonko/tablewriter@v0.0.5 # Due to breaking API in later versions

	# Enforce minimum go version
	go mod tidy -go=$(GOMIN)

	# Use the bundled toolchain that meets the minimum go version
	go get toolchain@none

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
	(cd / ; go install github.com/go-swagger/go-swagger/cmd/swagger@latest)
endif
	@# Generate spec and exclude package from dependency which causes a 'classifier: unknown swagger annotation "extendee"' error.
	@# For more details see: https://github.com/go-swagger/go-swagger/issues/2917.
	swagger generate spec -o doc/rest-api.yaml -w ./lxd -m -x github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2/options

.PHONY: update-metadata
update-metadata: build
	@echo "Generating golang documentation metadata"
	$(GOPATH)/bin/lxd-metadata . --json ./lxd/metadata/configuration.json --txt ./doc/config_options.txt

.PHONY: doc-setup
doc-setup: client
	@echo "Setting up documentation build environment"
	python3 -m venv doc/.sphinx/venv
	. $(SPHINXENV) ; pip install --upgrade -r doc/.sphinx/requirements.txt
	if [ -d "doc/reference/manpages/" ]; then find doc/reference/manpages/ -name "*.md" -type f -delete; fi
	rm -Rf doc/html
	rm -Rf doc/.sphinx/.doctrees

.PHONY: doc
doc: doc-setup doc-incremental

.PHONY: doc-incremental
doc-incremental:
	@echo "Build the documentation"
	. $(SPHINXENV) ; LOCAL_SPHINX_BUILD=True sphinx-build -c doc/ -b dirhtml doc/ doc/html/ -d doc/.sphinx/.doctrees -w doc/.sphinx/warnings.txt -j auto

.PHONY: doc-serve
doc-serve:
	cd doc/html; python3 -m http.server --bind 127.0.0.1 8001

.PHONY: doc-spellcheck
doc-spellcheck: doc
	. $(SPHINXENV) ; python3 -m pyspelling -c doc/.sphinx/spellingcheck.yaml -j $(shell nproc)

.PHONY: doc-linkcheck
doc-linkcheck: doc-setup
	. $(SPHINXENV) ; LOCAL_SPHINX_BUILD=True sphinx-build -c doc/ -b linkcheck doc/ doc/html/ -d doc/.sphinx/.doctrees -j auto

.PHONY: doc-lint
doc-lint:
	doc/.sphinx/.markdownlint/doc-lint.sh

.PHONY: doc-woke
doc-woke:
	type woke >/dev/null 2>&1 || { sudo snap install woke; }
	woke *.md **/*.md -c https://github.com/canonical/Inclusive-naming/raw/main/config.yml

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

.PHONY: check
check: default
ifeq "$(LXD_OFFLINE)" ""
	(cd / ; go install github.com/rogpeppe/godeps@latest)
	(cd / ; go install github.com/tsenart/deadcode@latest)
	(cd / ; go install golang.org/x/lint/golint@latest)
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

	# Download the dqlite library
	git clone --depth=1 --branch "${DQLITE_BRANCH}" https://github.com/canonical/dqlite $(TMP)/lxd-$(VERSION)/vendor/dqlite
	(cd $(TMP)/lxd-$(VERSION)/vendor/dqlite ; git show-ref HEAD | cut -d' ' -f1 > .gitref)

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

tags: */*.go
	find . -type f -name '*.go' | gotags -L - -f tags
