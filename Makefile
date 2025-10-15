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
GOMIN=1.25.4
GOTOOLCHAIN=local
export GOTOOLCHAIN
GOCOVERDIR ?= $(shell go env GOCOVERDIR)
ifeq "$(GOCOVERDIR)" ""
	COVER=
	COVER_TEST=
else
	COVER=-cover
	COVER_TEST=-test.gocoverdir="$(GOCOVERDIR)"
endif
ARCH ?= $(shell uname -m)
DQLITE_BRANCH=lts-1.17.x
LIBLXC_BRANCH=stable-6.0

ifneq "$(wildcard vendor)" ""
	DEPS_PATH=$(CURDIR)/vendor
else
	DEPS_PATH=$(GOPATH)/deps
endif
DQLITE_PATH=$(DEPS_PATH)/dqlite
LIBLXC_PATH=$(DEPS_PATH)/liblxc
LIBLXC_ROOTFS_MOUNT_PATH=$(GOPATH)/bin/liblxc/rootfs

export CGO_CFLAGS ?= -I$(DQLITE_PATH)/include/ -I$(LIBLXC_PATH)/include/
export CGO_LDFLAGS ?= -L$(DQLITE_PATH)/.libs/ -L$(LIBLXC_PATH)/lib/$(ARCH)-linux-gnu/
export LD_LIBRARY_PATH ?= $(DQLITE_PATH)/.libs/:$(LIBLXC_PATH)/lib/$(ARCH)-linux-gnu/
export PKG_CONFIG_PATH ?= $(LIBLXC_PATH)/lib/$(ARCH)-linux-gnu/pkgconfig
export CGO_LDFLAGS_ALLOW ?= (-Wl,-wrap,pthread_create)|(-Wl,-z,now)

.PHONY: default
default: all

.PHONY: build
build: lxd

.PHONY: all
all: client lxd lxd-agent lxd-benchmark lxd-metadata lxd-migrate lxd-user test-binaries

.PHONY: lxd
lxd:
ifeq "$(TAG_SQLITE3)" ""
	@echo "Missing dqlite, run \"make deps\" to setup."
	exit 1
endif
	CC="$(CC)" CGO_LDFLAGS_ALLOW="$(CGO_LDFLAGS_ALLOW)" go install -v -tags "$(TAG_SQLITE3)" -trimpath $(COVER) $(DEBUG) ./lxd
	@echo "$@ built successfully"

.PHONY: lxd-benchmark
lxd-benchmark:
	CGO_ENABLED=0 go install -v -tags netgo -trimpath $(COVER) $(DEBUG) ./lxd-benchmark
	@echo "$@ built successfully"

.PHONY: lxd-user
lxd-user:
	CGO_ENABLED=0 go install -v -tags netgo -trimpath $(COVER) $(DEBUG) ./lxd-user
	@echo "$@ built successfully"

.PHONY: client
client:
	go install -v -trimpath $(COVER) $(DEBUG) ./lxc
	@echo "LXD $@ built successfully"

.PHONY: lxd-agent
lxd-agent:
	CGO_ENABLED=0 go install -v -trimpath $(COVER) -tags agent,netgo ./lxd-agent
	@echo "$@ built successfully"

.PHONY: lxd-metadata
lxd-metadata:
	CGO_ENABLED=0 go install -v -trimpath $(COVER) -tags lxd-metadata ./lxd/lxd-metadata
	@echo "$@ built successfully"

.PHONY: lxd-migrate
lxd-migrate:
	CGO_ENABLED=0 go install -v -trimpath $(COVER) -tags netgo ./lxd-migrate
	@echo "$@ built successfully"

.PHONY: devlxd-client
devlxd-client:
	CGO_ENABLED=0 go install -C test -v -trimpath -buildvcs=false $(COVER) -tags netgo ./devlxd-client
	@echo "$@ built successfully"

.PHONY: fuidshift
fuidshift:
	go install -v -trimpath -buildvcs=false $(COVER) ./fuidshift
	@echo "$@ built successfully"

.PHONY: mini-oidc
mini-oidc:
	go install -C test -v -trimpath -buildvcs=false $(COVER) ./mini-oidc
	@echo "$@ built successfully"

.PHONY: sysinfo
sysinfo:
	go install -C test -v -trimpath -buildvcs=false $(COVER) ./syscall/sysinfo
	@echo "$@ built successfully"

.PHONY: test-binaries
test-binaries: devlxd-client fuidshift mini-oidc sysinfo
	@echo "$@ built successfully"

.PHONY: dqlite
dqlite:
	# dqlite (+raft)
	@if [ ! -e "$(DQLITE_PATH)" ]; then \
		echo "Retrieving dqlite from ${DQLITE_BRANCH} branch"; \
		git clone --depth=1 --branch "${DQLITE_BRANCH}" "https://github.com/canonical/dqlite" "$(DQLITE_PATH)"; \
	elif [ -e "$(DQLITE_PATH)/.git" ]; then \
		if [ "$(shell git -C "$(DQLITE_PATH)" branch --show-current)" = "master" ]; then \
			echo "Update your local checkout of dqlite to use the 'main' branch instead of the 'master'"; \
			exit 1; \
		fi; \
		echo "Updating existing dqlite branch"; \
		git -C "$(DQLITE_PATH)" pull; \
	fi

	cd "$(DQLITE_PATH)" && \
		autoreconf -i && \
		./configure --enable-build-raft && \
		make -j

.PHONY: liblxc
liblxc:
	# lxc/liblxc
	@if [ ! -e "$(LIBLXC_PATH)" ]; then \
		echo "Retrieving lxc/liblxc from $(LIBLXC_BRANCH) branch"; \
		git clone --depth=1 --branch "${LIBLXC_BRANCH}" "https://github.com/lxc/lxc" "$(LIBLXC_PATH)"; \
	elif [ -e "$(LIBLXC_PATH)/.git" ]; then \
		echo "Updating existing lxc/liblxc branch"; \
		git -C "$(LIBLXC_PATH)" pull; \
	fi

	# XXX: the rootfs-mount-path must not depend on LIBLXC_PATH to allow
	# building in "vendor" mode but move the resulting binaries elsewhere for
	# caching purposes
	cd "$(LIBLXC_PATH)" && \
		meson setup \
			--buildtype=release \
			-Dapparmor=true \
			-Dcapabilities=true \
			-Dcommands=false \
			-Ddbus=false \
			-Dexamples=false \
			-Dinstall-init-files=false \
			-Dinstall-state-dirs=false \
			-Dlibdir="lib/$(ARCH)-linux-gnu" \
			-Dman=false \
			-Dmemfd-rexec=false \
			-Dopenssl=false \
			-Dprefix="$(LIBLXC_PATH)" \
			-Drootfs-mount-path="$(LIBLXC_ROOTFS_MOUNT_PATH)" \
			-Dseccomp=true \
			-Dselinux=false \
			-Dspecfile=false \
			-Dtests=false \
			-Dtools=false \
			build && \
		meson compile -C build && \
		ninja -C build install

ifneq ($(shell command -v ldd),)
	# verify that liblxc.so is linked against some critically important libs
	ldd "$(LIBLXC_PATH)/lib/$(ARCH)-linux-gnu/liblxc.so" | grep -wE 'libapparmor|libcap|libseccomp'
	[ "$$(ldd "$(LIBLXC_PATH)/lib/$(ARCH)-linux-gnu/liblxc.so" | grep -cwE 'libapparmor|libcap|libseccomp')" = "3" ]
	@echo "OK: liblxc .so link check passed"
endif

.PHONY: env
env:
	@echo "export CGO_CFLAGS=\"$(CGO_CFLAGS)\""
	@echo "export CGO_LDFLAGS=\"$(CGO_LDFLAGS)\""
	@echo "export LD_LIBRARY_PATH=\"$(LD_LIBRARY_PATH)\""
	@echo "export PKG_CONFIG_PATH=\"$(PKG_CONFIG_PATH)\""
	@echo "export CGO_LDFLAGS_ALLOW=\"$(CGO_LDFLAGS_ALLOW)\""

.PHONY: deps
deps: dqlite liblxc
	@echo ""
	@echo "# Please set the following in your environment (possibly ~/.bashrc)"
	@$(MAKE) -s env

# Spawns an interactive test shell for quick interactions with LXD and the test
# suite.
.PHONY: test-shell
test-shell:
	@eval "$(MAKE) -s env"
	cd test && exec ./main.sh test-shell

.PHONY: tics
tics: deps
	go build -a -x -v ./...
	CC="cc" CGO_LDFLAGS_ALLOW="(-Wl,-wrap,pthread_create)|(-Wl,-z,now)" go install -v -tags "libsqlite3" -trimpath -a -x -v ./...

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

	@# Update doc/requirements.md and .github/copilot-instructions.md
	sed -i 's/^\(LXD requires Go \)[0-9.]\+ /\1$(NEW_GOMIN) /' doc/requirements.md .github/copilot-instructions.md

	@echo "Go minimum version updated to $(NEW_GOMIN)"
	if [ -t 0 ]; then \
		read -rp "Would you like to commit Go version changes (Y/n)? " answer; \
		if [ "$${answer:-y}" = "y" ] || [ "$${answer:-y}" = "Y" ]; then \
			git commit -S -sm "go: Update Go minimum version to $(NEW_GOMIN)" -- ./Makefile ./doc/requirements.md ./.github/copilot-instructions.md; \
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
	$(MAKE) check-gomin

	# Use the bundled toolchain that meets the minimum go version
	go get toolchain@none

	@echo "Dependencies updated"
	if [ -t 0 ] && ! git diff --quiet -- ./go.mod ./go.sum; then \
		read -rp "Would you like to commit gomod changes (Y/n)? " answer; \
		if [ "$${answer:-y}" = "y" ] || [ "$${answer:-y}" = "Y" ]; then \
			git commit -S -sm "gomod: Update dependencies" -- ./go.mod ./go.sum; \
		fi; \
	fi


.PHONY: update-protobuf
update-protobuf:
	protoc --go_out=. ./lxd/migration/migrate.proto

.PHONY: update-schema
update-schema:
	@# XXX: `go install ...@latest` is almost a noop if already up to date
	go install golang.org/x/tools/cmd/goimports@latest
	go build -C lxd/db/generate -v -trimpath -o $(GOPATH)/bin/lxd-generate -tags "$(TAG_SQLITE3)" $(DEBUG)
	go generate ./...
	@echo "Code generation completed"

.PHONY: update-api
update-api:
ifeq "$(LXD_OFFLINE)" ""
	go install github.com/go-swagger/go-swagger/cmd/swagger@latest
endif
	@# Generate spec and exclude package from dependency which causes a 'classifier: unknown swagger annotation "extendee"' error.
	@# For more details see: https://github.com/go-swagger/go-swagger/issues/2917.
	swagger generate spec -o doc/rest-api.yaml -w ./lxd -m -x github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2/options
	if [ -t 0 ] && ! git diff --quiet -- ./doc/rest-api.yaml; then \
		read -rp "Would you like to commit swagger YAML changes (Y/n)? " answer; \
		if [ "$${answer:-y}" = "y" ] || [ "$${answer:-y}" = "Y" ]; then \
			git commit -S -sm "doc/rest-api: Refresh swagger YAML" -- ./doc/rest-api.yaml; \
		fi; \
	fi

.PHONY: update-metadata
update-metadata: lxd-metadata
	@echo "Generating golang documentation metadata"
	$(GOPATH)/bin/lxd-metadata . --json ./lxd/metadata/configuration.json --txt ./doc/metadata.txt --substitution-db ./doc/substitutions.yaml
	if [ -t 0 ] && ! git diff --quiet -- ./lxd/metadata/configuration.json ./doc/metadata.txt; then \
		read -rp "Would you like to commit metadata changes (Y/n)? " answer; \
		if [ "$${answer:-y}" = "y" ] || [ "$${answer:-y}" = "Y" ]; then \
			git commit -S -sm "doc: Update metadata" -- ./lxd/metadata/configuration.json ./doc/metadata.txt; \
		fi; \
	fi

.PHONY: update-godeps
update-godeps:
	@echo "Updating godeps.list files"
	@UPDATE_LISTS=true test/lint/godeps.sh

.PHONY: doc
doc: doc-clean doc-install doc-html doc-objects

.PHONY: doc-incremental
doc-incremental: doc-html

doc-%:
	$(MAKE) -C doc -f Makefile $*

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
check: default check-gomin check-unit test-binaries
	cd test && ./main.sh

.PHONY: check-unit
check-unit:
	CGO_LDFLAGS_ALLOW="$(CGO_LDFLAGS_ALLOW)" go test -mod=readonly -v -failfast -tags "$(TAG_SQLITE3)" $(DEBUG) ./... $(COVER) $(COVER_TEST)

.PHONY: dist
dist:
	# Cleanup
	rm -f $(ARCHIVE).gz

	# Create build dir
	$(eval TMP := $(shell mktemp -d))
	$(eval COMMIT_HASH := $(shell git rev-parse HEAD))
	git archive --prefix=lxd-$(VERSION)/ $(COMMIT_HASH) | tar -x -C $(TMP)
	echo $(COMMIT_HASH) > $(TMP)/lxd-$(VERSION)/.gitref

	# Download dependencies
	(cd $(TMP)/lxd-$(VERSION) ; go mod vendor)

	# Download the dqlite library
	git clone --depth=1 --branch "$(DQLITE_BRANCH)" https://github.com/canonical/dqlite $(TMP)/lxd-$(VERSION)/vendor/dqlite
	(cd $(TMP)/lxd-$(VERSION)/vendor/dqlite ; git rev-parse HEAD | tee .gitref)

	# Download the liblxc library
	git clone --depth=1 --branch "$(LIBLXC_BRANCH)" https://github.com/lxc/lxc $(TMP)/lxd-$(VERSION)/vendor/liblxc
	(cd $(TMP)/lxd-$(VERSION)/vendor/liblxc ; git rev-parse HEAD | tee .gitref)

	# Do not build doc on `make dist` on GH PRs
	if [ "$(GITHUB_EVENT_NAME)" = "pull_request" ]; then \
		echo "Skipping doc generation for 'make dist' on pull_request event"; \
	else \
		$(MAKE) doc; \
		cp -r --preserve=mode doc/_build $(TMP)/lxd-$(VERSION)/doc/html/; \
	fi

	# Assemble a reproducible tarball
	# The reproducibility comes from:
	# * predictable file sorting (`--sort=name`)
	# * clamping mtime to that of the HEAD commit timestamp
	# * omit irrelevant information about file access or status change time
	# * omit irrelevant information about user and group names
	# * omit irrelevant information about file ownership and group
	# * tell `gzip` to not embed the file name when compressing
	# For more details: https://www.gnu.org/software/tar/manual/html_node/Reproducibility.html
	$(eval SOURCE_EPOCH := $(shell TZ=UTC0 git log -1 --format=tformat:%cd --date=format:%Y-%m-%dT%H:%M:%SZ $(COMMIT_HASH)))
	LC_ALL=C tar --sort=name --format=posix \
		--pax-option=exthdr.name=%d/PaxHeaders/%f \
		--pax-option=delete=atime,delete=ctime \
		--clamp-mtime --mtime=$(SOURCE_EPOCH) \
		--numeric-owner --owner=0 --group=0 \
		--mode=go+u,go-w \
		--use-compress-program="gzip --no-name" \
		--exclude-vcs -C $(TMP) -cf $(ARCHIVE).gz lxd-$(VERSION)/

	# Cleanup
	rm -Rf $(TMP)

.PHONY: i18n
i18n: update-pot update-po

po/%.mo: po/%.po
	msgfmt --statistics -o $@ $<

po/%.po: po/$(DOMAIN).pot
	msgmerge --silent -U po/$*.po po/$(DOMAIN).pot

.PHONY: update-po
update-po:
	set -eu; \
	for lang in $(LINGUAS); do \
		msgmerge --silent --backup=none -U $$lang.po po/$(DOMAIN).pot; \
	done; \
	if [ -t 0 ] && ! git diff --quiet -- po/*.po; then \
		read -rp "Would you like to commit i18n changes (Y/n)? " answer; \
			if [ "$${answer:-y}" = "y" ] || [ "$${answer:-y}" = "Y" ]; then \
				git commit -S -sm "i18n: Update translations." -- po/*.po; \
			fi; \
	fi

.PHONY: update-pot
update-pot:
ifeq "$(LXD_OFFLINE)" ""
	@# XXX: `go install ...@latest` is almost a noop if already up to date
	@# Cannot use newer versions (2.58 to 2.72 all failed)
	go install github.com/snapcore/snapd/i18n/xgettext-go@2.57.6
endif
	xgettext-go -o po/$(DOMAIN).pot --add-comments-tag=TRANSLATORS: --sort-output --package-name=$(DOMAIN) --msgid-bugs-address=lxd@lists.canonical.com --keyword=i18n.G --keyword-plural=i18n.NG lxc/*.go lxc/*/*.go
	if git diff --quiet --ignore-matching-lines='^\s*"POT-Creation-Date: .*\n"' -- po/*.pot; then git checkout -- po/*.pot; fi
	if [ -t 0 ] && ! git diff --quiet --ignore-matching-lines='^\s*"POT-Creation-Date: .*\n"' -- po/*.pot; then \
		read -rp "Would you like to commit i18n template changes (Y/n)? " answer; \
			if [ "$${answer:-y}" = "y" ] || [ "$${answer:-y}" = "Y" ]; then \
				git commit -S -sm "i18n: Update translation templates." -- po/*.pot; \
			fi; \
	fi

.PHONY: build-mo
build-mo: $(MOFILES)

.PHONY: static-analysis
static-analysis:
ifeq "$(LXD_OFFLINE)" ""
	@# XXX: `go install ...@latest` is almost a noop if already up to date
	go install github.com/google/go-licenses@latest

	@# XXX: if errortype becomes available as a golangci-lint linter, remove this and update golangci-lint config
	go install fillmore-labs.com/errortype@latest

	@# XXX: if zerolint becomes available as a golangci-lint linter, remove this and update golangci-lint config
	go install fillmore-labs.com/zerolint@latest
endif
ifeq ($(shell command -v golangci-lint),)
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(GOPATH)/bin latest
endif
ifneq ($(shell command -v yamllint),)
	yamllint .github/workflows/*.yml
endif
ifeq ($(shell command -v shellcheck),)
	echo "Please install shellcheck"
	exit 1
endif
	@echo "Verify test/lint files are properly named and executable"
	@NOT_EXEC="$(shell find test/lint -type f -not -executable)"; \
	if [ -n "$$NOT_EXEC" ]; then \
		echo "lint scripts not executable: $$NOT_EXEC"; \
		exit 1; \
	fi
	@BAD_NAME="$(shell find test/lint -type f -not -name '*.sh')"; \
	if [ -n "$$BAD_NAME" ]; then \
		echo "lint scripts missing .sh extension: $$BAD_NAME"; \
		exit 1; \
	fi
	run-parts --verbose --exit-on-error --regex '.sh' test/lint

.PHONY: update-auth
update-auth:
	go generate ./lxd/auth
	if [ -t 0 ] && ! git diff --quiet -- ./lxd/auth/; then \
		read -rp "Would you like to commit auth changes (Y/n)? " answer; \
		if [ "$${answer:-y}" = "y" ] || [ "$${answer:-y}" = "Y" ]; then \
			git commit -S -sm "lxd/auth: Update auth" -- ./lxd/auth/; \
		fi; \
	fi

.PHONY: update-fmt
update-fmt:
	gofmt -w -s ./
