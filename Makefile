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
RAFT_REF=abf9c42a9bb63c24920ab9f0bfbc4b7a47e7e5f4
DQLITE_REF=50ee9af350b2fb4e79f9eb58db22c8a0927138de
# Keep LIBLXC_REF and LIBLXC_PATCHES in sync with the lxc part in snap/snapcraft.yaml.
LIBLXC_REF=lxc-4.0.12
LIBLXC_PATCHES=ca4c25c6e9ebb92f9e241ef9a231d4a715cb812d:f314419d1e054f7833b6976ec5ed32373aace622:71ba7f65616e72a313e2a41615e449178da9daf2:f7446b4e10d71f79f9f3952255608268842ee1f3:f1c4a17e7df5d819b1b170917865e2e458c8e5db:8ef019a6ce2555f7b438b3841ab5216e5d6973ba:3b9f84fd2397d06782bbf67dc8421463c43ab139:5ba5725cb4a210c25707beeca64fde5f561d1c71

ifneq "$(wildcard vendor)" ""
	DEPS_PATH=$(CURDIR)/vendor
else
	DEPS_PATH=$(GOPATH)/deps
endif
RAFT_PATH=$(DEPS_PATH)/raft
DQLITE_PATH=$(DEPS_PATH)/dqlite
LIBLXC_PATH=$(DEPS_PATH)/liblxc
LIBLXC_ROOTFS_MOUNT_PATH=$(LIBLXC_PATH)/rootfs
LIBLXC_INSTALL_STAGE=$(LIBLXC_PATH)/install-stage

export CGO_CFLAGS ?= -I$(RAFT_PATH)/include/ -I$(DQLITE_PATH)/include/ -I$(LIBLXC_PATH)/include/
export CGO_LDFLAGS ?= -L$(RAFT_PATH)/.libs/ -L$(DQLITE_PATH)/.libs/ -L$(LIBLXC_PATH)/lib/
export LD_LIBRARY_PATH ?= $(RAFT_PATH)/.libs/:$(DQLITE_PATH)/.libs/:$(LIBLXC_PATH)/lib/
export PKG_CONFIG_PATH ?= $(LIBLXC_PATH)/lib/pkgconfig
export CGO_LDFLAGS_ALLOW ?= (-Wl,-wrap,pthread_create)|(-Wl,-z,now)
TAG_SQLITE3=$(shell printf "$(HASH)include <dqlite.h>\nvoid main(){dqlite_node_id n = 1;}" | $(CC) ${CGO_CFLAGS} -o /dev/null -xc - >/dev/null 2>&1 && echo "libsqlite3")

.PHONY: default
default: build

.PHONY: build
build:
ifeq "$(TAG_SQLITE3)" ""
	@echo "Missing dqlite, run \"make deps\" to setup."
	exit 1
endif

	CC="$(CC)" CGO_LDFLAGS_ALLOW="$(CGO_LDFLAGS_ALLOW)" go install -v -tags "$(TAG_SQLITE3)" $(DEBUG) ./...
	CGO_ENABLED=0 go install -v -tags netgo ./lxd-p2c
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

.PHONY: lxd-p2c
lxd-p2c:
	CGO_ENABLED=0 go install -v -tags netgo ./lxd-p2c
	@echo "LXD-P2C built successfully"

.PHONY: raft
raft:
	# raft
	@if [ ! -e "$(RAFT_PATH)" ]; then \
		git clone "https://github.com/canonical/raft" "$(RAFT_PATH)"; \
	fi
	git -C "$(RAFT_PATH)" fetch origin
	git -C "$(RAFT_PATH)" checkout --detach "$(RAFT_REF)"

	@if [ ! -f "$(RAFT_PATH)/Makefile" ]; then \
		cd "$(RAFT_PATH)" && \
			autoreconf -i && \
			./configure; \
	fi
	cd "$(RAFT_PATH)" && make -j

	@if command -v ldd >/dev/null 2>&1; then \
		ldd "$(RAFT_PATH)/.libs/libraft.so" | grep -wE 'liblz4|libuv'; \
		[ "$$(ldd "$(RAFT_PATH)/.libs/libraft.so" | grep -cwE 'liblz4|libuv')" = "2" ]; \
		echo "OK: libraft .so link check passed"; \
	fi

.PHONY: dqlite
dqlite:
	# dqlite
	@if [ ! -e "$(DQLITE_PATH)" ]; then \
		git clone "https://github.com/canonical/dqlite" "$(DQLITE_PATH)"; \
	fi
	git -C "$(DQLITE_PATH)" fetch origin
	git -C "$(DQLITE_PATH)" checkout --detach "$(DQLITE_REF)"

	@if [ ! -f "$(DQLITE_PATH)/Makefile" ]; then \
		cd "$(DQLITE_PATH)" && \
			autoreconf -i && \
			PKG_CONFIG_PATH="$(RAFT_PATH)" ./configure; \
	fi
	cd "$(DQLITE_PATH)" && make -j CFLAGS="-I$(RAFT_PATH)/include/" LDFLAGS="-L$(RAFT_PATH)/.libs/"

	@if command -v ldd >/dev/null 2>&1; then \
		ldd "$(DQLITE_PATH)/.libs/libdqlite.so" | grep -wE 'libraft|libsqlite3|libuv'; \
		[ "$$(ldd "$(DQLITE_PATH)/.libs/libdqlite.so" | grep -cwE 'libraft|libsqlite3|libuv')" = "3" ]; \
		echo "OK: libdqlite .so link check passed"; \
	fi

.PHONY: liblxc
liblxc:
	# lxc/liblxc
	@if [ ! -e "$(LIBLXC_PATH)" ]; then \
		git clone "https://github.com/lxc/lxc" "$(LIBLXC_PATH)"; \
	fi
	git -C "$(LIBLXC_PATH)" fetch origin
	git -C "$(LIBLXC_PATH)" checkout --detach "$(LIBLXC_REF)"
	@for patch in $(subst :, ,$(LIBLXC_PATCHES)); do \
		git -C "$(LIBLXC_PATH)" -c user.name=lxd-deps -c user.email=noreply@lists.canonical.com cherry-pick "$${patch}"; \
	done

	@mkdir -p "$(LIBLXC_ROOTFS_MOUNT_PATH)"
	@if [ ! -f "$(LIBLXC_PATH)/Makefile" ]; then \
		cd "$(LIBLXC_PATH)" && \
			./autogen.sh && \
			./configure \
				--prefix="$(LIBLXC_PATH)" \
				--disable-api-docs \
				--disable-bash \
				--disable-doc \
				--disable-examples \
				--disable-memfd-rexec \
				--disable-tests \
				--disable-tools \
				--enable-apparmor \
				--enable-seccomp \
				--enable-selinux \
				--enable-capabilities \
				--with-rootfs-path="$(LIBLXC_ROOTFS_MOUNT_PATH)" \
				--libexecdir="$(LIBLXC_PATH)/libexec"; \
	fi
	@rm -rf "$(LIBLXC_INSTALL_STAGE)"
	cd "$(LIBLXC_PATH)" && make -j && DESTDIR="$(LIBLXC_INSTALL_STAGE)" make install
	cp -a "$(LIBLXC_INSTALL_STAGE)$(LIBLXC_PATH)/." "$(LIBLXC_PATH)/"
	@rm -rf "$(LIBLXC_INSTALL_STAGE)"

	@if command -v ldd >/dev/null 2>&1; then \
		ldd "$(LIBLXC_PATH)/lib/liblxc.so" | grep -wE 'libcap|libseccomp|libselinux'; \
		[ "$$(ldd "$(LIBLXC_PATH)/lib/liblxc.so" | grep -cwE 'libcap|libseccomp|libselinux')" = "3" ]; \
		echo "OK: liblxc .so link check passed"; \
	fi

.PHONY: env
env:
	@echo "export CGO_CFLAGS=\"$(CGO_CFLAGS)\""; \
	 echo "export CGO_LDFLAGS=\"$(CGO_LDFLAGS)\""; \
	 echo "export LD_LIBRARY_PATH=\"$(LD_LIBRARY_PATH)\""; \
	 echo "export PKG_CONFIG_PATH=\"$(PKG_CONFIG_PATH)\""; \
	 echo "export CGO_LDFLAGS_ALLOW=\"$(CGO_LDFLAGS_ALLOW)\""

.PHONY: deps
deps: raft dqlite liblxc
	@echo ""; echo "# Please set the following in your environment (possibly ~/.bashrc)"
	@$(MAKE) -s env

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
	(cd / ; GO111MODULE=on go install -v -x github.com/go-swagger/go-swagger/cmd/swagger)
endif
	swagger generate spec -o doc/rest-api.yaml -w ./lxd -m

.PHONY: doc
doc:
	@echo "Setting up documentation build environment"
	python3 -m venv doc/.sphinx/venv
	. $(SPHINXENV) ; pip install --upgrade -r doc/.sphinx/requirements.txt
	rm -Rf doc/html
	make doc-incremental

.PHONY: doc-incremental
doc-incremental:
	@echo "Build the documentation"
	. $(SPHINXENV) ; sphinx-build -c doc/ -b dirhtml doc/ doc/html/ -w doc/.sphinx/warnings.txt

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
	CGO_ENABLED=0 go install -v -tags "netgo,logdebug" ./lxd-p2c
	CGO_ENABLED=0 go install -v -tags "agent,netgo,logdebug" ./lxd-agent
	@echo "LXD built successfully"

.PHONY: nocache
nocache:
ifeq "$(TAG_SQLITE3)" ""
	@echo "Missing custom libsqlite3, run \"make deps\" to setup."
	exit 1
endif

	CC="$(CC)" CGO_LDFLAGS_ALLOW="$(CGO_LDFLAGS_ALLOW)" go install -a -v -tags "$(TAG_SQLITE3)" $(DEBUG) ./...
	CGO_ENABLED=0 go install -a -v -tags netgo ./lxd-p2c
	CGO_ENABLED=0 go install -a -v -tags agent,netgo ./lxd-agent
	@echo "LXD built successfully"

race:
ifeq "$(TAG_SQLITE3)" ""
	@echo "Missing custom libsqlite3, run \"make deps\" to setup."
	exit 1
endif

	CC="$(CC)" CGO_LDFLAGS_ALLOW="$(CGO_LDFLAGS_ALLOW)" go install -race -v -tags "$(TAG_SQLITE3)" $(DEBUG) ./...
	CGO_ENABLED=0 go install -v -tags netgo ./lxd-p2c
	CGO_ENABLED=0 go install -v -tags agent,netgo ./lxd-agent
	@echo "LXD built successfully"

.PHONY: check
check: default
ifeq "$(LXD_OFFLINE)" ""
	(cd / ; go install -v -x github.com/rogpeppe/godeps)
	(cd / ; go install -v -x github.com/tsenart/deadcode)
	(cd / ; go install -v -x golang.org/x/lint/golint)
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

	# Download the dqlite libraries
	git clone https://github.com/canonical/dqlite $(TMP)/lxd-$(VERSION)/vendor/dqlite
	(cd $(TMP)/lxd-$(VERSION)/vendor/dqlite ; git reset --hard "$(DQLITE_REF)")
	(cd $(TMP)/lxd-$(VERSION)/vendor/dqlite ; git show-ref HEAD | cut -d' ' -f1 > .gitref)

	git clone https://github.com/canonical/raft $(TMP)/lxd-$(VERSION)/vendor/raft
	(cd $(TMP)/lxd-$(VERSION)/vendor/raft ; git reset --hard "$(RAFT_REF)")
	(cd $(TMP)/lxd-$(VERSION)/vendor/raft ; git show-ref HEAD | cut -d' ' -f1 > .gitref)

	# Download the liblxc library
	git clone https://github.com/lxc/lxc $(TMP)/lxd-$(VERSION)/vendor/liblxc
	(cd $(TMP)/lxd-$(VERSION)/vendor/liblxc ; git checkout --detach "$(LIBLXC_REF)")
	@for patch in $(subst :, ,$(LIBLXC_PATCHES)); do \
		git -C $(TMP)/lxd-$(VERSION)/vendor/liblxc -c user.name=lxd-deps -c user.email=noreply@lists.canonical.com cherry-pick "$${patch}"; \
	done
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
	for lang in $(LINGUAS); do\
	    msgmerge -U $$lang.po po/$(DOMAIN).pot; \
	    rm -f $$lang.po~; \
	done

.PHONY: update-pot
update-pot:
ifeq "$(LXD_OFFLINE)" ""
	(cd / ; go install -v -x github.com/snapcore/snapd/i18n/xgettext-go@2.57.1)
endif
	xgettext-go -o po/$(DOMAIN).pot --add-comments-tag=TRANSLATORS: --sort-output --package-name=$(DOMAIN) --msgid-bugs-address=lxd@lists.canonical.com --keyword=i18n.G --keyword-plural=i18n.NG lxc/*.go lxc/*/*.go

.PHONY: build-mo
build-mo: $(MOFILES)

.PHONY: static-analysis
static-analysis:
	(cd test; sh -c ". suites/static_analysis.sh; test_static_analysis")

.PHONY: tags
tags: *.go lxd/*.go shared/*.go lxc/*.go
	find . | grep \.go | grep -v git | grep -v .swp | grep -v vagrant | xargs gotags > tags
