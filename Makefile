DOMAIN=lxd
POFILES=$(wildcard po/*.po)
MOFILES=$(patsubst %.po,%.mo,$(POFILES))
LINGUAS=$(basename $(POFILES))
POTFILE=po/$(DOMAIN).pot
VERSION=$(or ${CUSTOM_VERSION},$(shell grep "var Version" shared/version/flex.go | cut -d'"' -f2))
ARCHIVE=lxd-$(VERSION).tar
HASH := \#
GOPATH ?= $(shell go env GOPATH)
CGO_LDFLAGS_ALLOW ?= (-Wl,-wrap,pthread_create)|(-Wl,-z,now)
SPHINXENV=doc/.sphinx/venv/bin/activate
SPHINXPIPPATH=doc/.sphinx/venv/bin/pip
GOMIN=1.27.4
GOTOOLCHAIN=local
export GOTOOLCHAIN
GOCOVERDIR ?= $(shell go env GOCOVERDIR)
ARCH ?= $(shell uname -m)
DQLITE_BRANCH=lts-1.17.x
LIBLXC_BRANCH=lxc-5.0.3

ifneq "$(wildcard vendor)" ""
	DEPS_PATH=$(CURDIR)/vendor
else
	DEPS_PATH=$(GOPATH)/deps
endif
DQLITE_PATH=$(DEPS_PATH)/dqlite
LIBLXC_PATH=$(DEPS_PATH)/liblxc
LIBLXC_ROOTFS_MOUNT_PATH=$(LIBLXC_PATH)/rootfs
LIBLXC_INSTALL_STAGE=$(LIBLXC_PATH)/install-stage

export CGO_CFLAGS ?= -I$(DQLITE_PATH)/include/ -I$(LIBLXC_PATH)/include/
export CGO_LDFLAGS ?= -L$(DQLITE_PATH)/.libs/ -L$(LIBLXC_PATH)/lib/$(ARCH)-linux-gnu/
export LD_LIBRARY_PATH ?= $(DQLITE_PATH)/.libs/:$(LIBLXC_PATH)/lib/$(ARCH)-linux-gnu/
export PKG_CONFIG_PATH ?= $(LIBLXC_PATH)/lib/$(ARCH)-linux-gnu/pkgconfig
export CGO_LDFLAGS_ALLOW ?= (-Wl,-wrap,pthread_create)|(-Wl,-z,now)
TAG_SQLITE3=$(shell printf "$(HASH)include <dqlite.h>\nvoid main(){dqlite_node_id n = 1;}" | $(CC) ${CGO_CFLAGS} -o /dev/null -xc - >/dev/null 2>&1 && echo "libsqlite3")

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

.PHONY: dqlite
dqlite:
	# dqlite (+raft)
	@if [ ! -e "$(DQLITE_PATH)" ]; then \
		echo "Retrieving dqlite from ${DQLITE_BRANCH} branch"; \
		git clone --depth=1 --branch "${DQLITE_BRANCH}" "https://github.com/canonical/dqlite" "$(DQLITE_PATH)"; \
	elif [ -e "$(DQLITE_PATH)/.git" ]; then \
		echo "Switching/updating dqlite to ${DQLITE_BRANCH} branch"; \
		git -C "$(DQLITE_PATH)" fetch --depth=1 origin "${DQLITE_BRANCH}"; \
		git -C "$(DQLITE_PATH)" checkout -B "${DQLITE_BRANCH}" FETCH_HEAD; \
	fi

	@if [ ! -f "$(DQLITE_PATH)/Makefile" ]; then \
		cd "$(DQLITE_PATH)" && \
			autoreconf -i && \
			./configure --enable-build-raft; \
	fi
	cd "$(DQLITE_PATH)" && make -j

ifneq ($(shell command -v ldd),)
	# verify that libdqlite.so is linked against some critically important libs
	ldd "$(DQLITE_PATH)/.libs/libdqlite.so" | grep -wE 'liblz4|libsqlite3|libuv'
	[ "$$(ldd "$(DQLITE_PATH)/.libs/libdqlite.so" | grep -cwE 'liblz4|libsqlite3|libuv')" = "3" ]
	@echo "OK: libdqlite .so link check passed"
endif

.PHONY: liblxc
liblxc:
	# lxc/liblxc
	@if [ ! -e "$(LIBLXC_PATH)" ]; then \
		echo "Retrieving lxc/liblxc from $(LIBLXC_BRANCH) branch"; \
		git clone --depth=1 --branch "$(LIBLXC_BRANCH)" "https://github.com/lxc/lxc" "$(LIBLXC_PATH)"; \
	elif [ -e "$(LIBLXC_PATH)/.git" ]; then \
		echo "Switching/updating lxc/liblxc to $(LIBLXC_BRANCH) branch"; \
		git -C "$(LIBLXC_PATH)" fetch --depth=1 origin "$(LIBLXC_BRANCH)"; \
		git -C "$(LIBLXC_PATH)" checkout -B "$(LIBLXC_BRANCH)" FETCH_HEAD; \
	fi

	@mkdir -p "$(LIBLXC_ROOTFS_MOUNT_PATH)"
	@if [ ! -d "$(LIBLXC_PATH)/build" ]; then \
		cd "$(LIBLXC_PATH)" && \
			meson setup \
				--buildtype=release \
				-Dapparmor=true \
				-Dcapabilities=true \
				-Dcommands=false \
				-Dexamples=false \
				-Dinit-script=[] \
				-Dlibdir="lib/$(ARCH)-linux-gnu" \
				-Dman=false \
				-Dmemfd-rexec=false \
				-Dopenssl=false \
				-Dprefix="$(LIBLXC_PATH)" \
				-Drootfs-mount-path="$(LIBLXC_ROOTFS_MOUNT_PATH)" \
				-Dsd-bus=disabled \
				-Dseccomp=true \
				-Dselinux=false \
				-Dtests=false \
				-Dtools=false \
				build; \
	fi
	# LXC 5.0 installs bash completion to an absolute /usr path, so stage the
	# installation to avoid requiring privileges for make deps.
	@rm -rf "$(LIBLXC_INSTALL_STAGE)"
	cd "$(LIBLXC_PATH)" && DESTDIR="$(LIBLXC_INSTALL_STAGE)" ninja -C build install
	cp -a "$(LIBLXC_INSTALL_STAGE)$(LIBLXC_PATH)/." "$(LIBLXC_PATH)/"
	@rm -rf "$(LIBLXC_INSTALL_STAGE)"

ifneq ($(shell command -v ldd),)
	# verify that liblxc.so is linked against some critically important libs
	ldd "$(LIBLXC_PATH)/lib/$(ARCH)-linux-gnu/liblxc.so" | grep -wE 'libapparmor|libcap|libseccomp'
	[ "$$(ldd "$(LIBLXC_PATH)/lib/$(ARCH)-linux-gnu/liblxc.so" | grep -cwE 'libapparmor|libcap|libseccomp')" = "3" ]
	@echo "OK: liblxc .so link check passed"
endif

.PHONY: env
env:
	@echo "export CGO_CFLAGS=\"$(CGO_CFLAGS)\""; \
	 echo "export CGO_LDFLAGS=\"$(CGO_LDFLAGS)\""; \
	 echo "export LD_LIBRARY_PATH=\"$(LD_LIBRARY_PATH)\""; \
	 echo "export PKG_CONFIG_PATH=\"$(PKG_CONFIG_PATH)\""; \
	 echo "export CGO_LDFLAGS_ALLOW=\"$(CGO_LDFLAGS_ALLOW)\""

.PHONY: deps
deps: dqlite liblxc
	@echo ""; echo "# Please set the following in your environment (possibly ~/.bashrc)"
	@$(MAKE) -s env

.PHONY: check-gomin
check-gomin:
	go mod tidy -go=$(GOMIN)
	@echo "Check the doc mentions the right Go minimum version"
	$(eval DOC_GOMIN := $(shell sed -n 's/^LXD requires Go \([0-9.]\+\) .*/\1/p' doc/requirements.md))
	if [ "$(DOC_GOMIN)" != "$(GOMIN)" ]; then \
		echo "Please update the Go version in 'doc/requirements.md' to be $(GOMIN) instead of $(DOC_GOMIN)"; \
		exit 1; \
	fi

.PHONY: update-gomin
update-gomin:
ifndef NEW_GOMIN
	@echo "Usage: make update-gomin NEW_GOMIN=1.x.y"
	@echo "Current Go minimum version: $(GOMIN)"
	exit 1
endif
ifeq "$(GOMIN)" "$(NEW_GOMIN)"
	@echo "Error: NEW_GOMIN ($(NEW_GOMIN)) is the same as current GOMIN ($(GOMIN))"
	exit 1
endif
	@echo "Updating Go minimum version from $(GOMIN) to $(NEW_GOMIN)"
	
	@# Update GOMIN in Makefile
	sed -i 's/^GOMIN=[0-9.]\+/GOMIN=$(NEW_GOMIN)/' Makefile

	@# Update GOMIN in go.mod
	sed -i 's/^go [0-9.]\+$$/go $(NEW_GOMIN)/' go.mod

	@# Update doc/requirements.md
	sed -i 's/^\(LXD requires Go \)[0-9.]\+ /\1$(NEW_GOMIN) /' doc/requirements.md

	@echo "Go minimum version updated to $(NEW_GOMIN)"
	if [ -t 0 ]; then \
		read -rp "Would you like to commit Go version changes (Y/n)? " answer; \
		if [ "$${answer:-y}" = "y" ] || [ "$${answer:-y}" = "Y" ]; then \
			git commit -S -sm "go: Update Go minimum version to $(NEW_GOMIN)" -- Makefile go.mod doc/requirements.md; \
		fi; \
	fi

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
	$(eval COMMIT_HASH := $(shell git rev-parse HEAD))
	# Export the source code at the current commit, excluding irrelevant files and directories
	git archive --prefix=lxd-$(VERSION)/ $(COMMIT_HASH) | tar -x -C $(TMP) --exclude=.gitignore --exclude=.github --exclude=grafana --exclude=tools
	echo $(COMMIT_HASH) > $(TMP)/lxd-$(VERSION)/.gitref

	# Download dependencies
	(cd $(TMP)/lxd-$(VERSION) ; go mod vendor)

	# Download the dqlite library
	git clone --depth=1 --branch "${DQLITE_BRANCH}" https://github.com/canonical/dqlite $(TMP)/lxd-$(VERSION)/vendor/dqlite
	(cd $(TMP)/lxd-$(VERSION)/vendor/dqlite ; git show-ref HEAD | cut -d' ' -f1 > .gitref)

	# Download the liblxc library
	git clone --depth=1 --branch "${LIBLXC_BRANCH}" https://github.com/lxc/lxc $(TMP)/lxd-$(VERSION)/vendor/liblxc
	(cd $(TMP)/lxd-$(VERSION)/vendor/liblxc ; git rev-parse HEAD | tee .gitref)

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
	curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b $(GOPATH)/bin latest
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
