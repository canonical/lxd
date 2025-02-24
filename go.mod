module github.com/canonical/lxd

go 1.22.7
toolchain go1.23.6

// https://github.com/minio/minio-go/issues/1931
replace github.com/minio/minio-go/v7 => github.com/minio/minio-go/v7 v7.0.66

require (
	github.com/Rican7/retry v0.3.1
	github.com/armon/go-proxyproto v0.1.0
	github.com/canonical/candid v1.12.3
	github.com/canonical/go-dqlite/v2 v2.0.0
	github.com/checkpoint-restore/go-criu/v6 v6.3.0
	github.com/digitalocean/go-qemu v0.0.0-20230711162256-2e3d0186973e
	github.com/digitalocean/go-smbios v0.0.0-20180907143718-390a4f403a8e
	github.com/dustinkirkland/golang-petname v0.0.0-20240428194347-eebcea082ee0
	github.com/flosch/pongo2 v0.0.0-20200913210552-0d938eb266f3
	github.com/fvbommel/sortorder v1.1.0
	github.com/go-macaroon-bakery/macaroon-bakery/v3 v3.0.2
	github.com/google/gopacket v1.1.19
	github.com/google/uuid v1.6.0
	github.com/gorilla/mux v1.8.1
	github.com/gorilla/websocket v1.5.1
	github.com/gosexy/gettext v0.0.0-20160830220431-74466a0a0c4a
	github.com/j-keck/arping v1.0.3
	github.com/jaypipes/pcidb v1.0.0
	github.com/jochenvg/go-udev v0.0.0-20240801134859-b65ed646224b
	github.com/juju/gomaasapi v0.0.0-20200602032615-aa561369c767
	github.com/juju/persistent-cookiejar v1.0.0
	github.com/kballard/go-shellquote v0.0.0-20180428030007-95032a82bc51
	github.com/lxc/go-lxc v0.0.0-20240606200241-27b3d116511f
	github.com/mattn/go-colorable v0.1.14
	github.com/mattn/go-sqlite3 v1.14.24
	github.com/mdlayher/ndp v1.1.0
	github.com/mdlayher/netx v0.0.0-20230430222610-7e21880baee8
	github.com/mdlayher/vsock v1.2.1
	github.com/miekg/dns v1.1.63
	github.com/minio/minio-go/v7 v7.0.81
	github.com/mitchellh/mapstructure v1.5.0
	github.com/olekukonko/tablewriter v0.0.5
	github.com/osrg/gobgp/v3 v3.34.0
	github.com/pkg/sftp v1.13.7
	github.com/pkg/xattr v0.4.10
	github.com/robfig/cron/v3 v3.0.1
	github.com/sirupsen/logrus v1.9.3
	github.com/spf13/cobra v1.9.1
	github.com/stretchr/testify v1.10.0
	github.com/syndtr/gocapability v0.0.0-20200815063812-42c35b437635
	github.com/zitadel/oidc/v2 v2.12.2
	golang.org/x/crypto v0.34.0
	golang.org/x/oauth2 v0.26.0
	golang.org/x/sync v0.11.0
	golang.org/x/sys v0.30.0
	golang.org/x/term v0.29.0
	golang.org/x/text v0.22.0
	google.golang.org/protobuf v1.36.5
	gopkg.in/juju/environschema.v1 v1.0.1
	gopkg.in/tomb.v2 v2.0.0-20161208151619-d5d1b5820637
	gopkg.in/yaml.v2 v2.4.0
	k8s.io/utils v0.0.0-20241104163129-6fe5fd82f078
)

require (
	github.com/cpuguy83/go-md2man/v2 v2.0.6 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/dgryski/go-farm v0.0.0-20240924180020-3414d57e47da // indirect
	github.com/digitalocean/go-libvirt v0.0.0-20221205150000-2939327a8519 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/eapache/channels v1.1.0 // indirect
	github.com/eapache/queue v1.1.0 // indirect
	github.com/fsnotify/fsnotify v1.8.0 // indirect
	github.com/go-macaroon-bakery/macaroonpb v1.0.0 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/renameio v1.0.1 // indirect
	github.com/gorilla/schema v1.4.1 // indirect
	github.com/gorilla/securecookie v1.1.2 // indirect
	github.com/hashicorp/hcl v1.0.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jkeiser/iter v0.0.0-20200628201005-c8aa0ae784d1 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/juju/collections v1.0.4 // indirect
	github.com/juju/errors v1.0.0 // indirect
	github.com/juju/go4 v0.0.0-20160222163258-40d72ab9641a // indirect
	github.com/juju/loggo v1.0.0 // indirect
	github.com/juju/mgo/v2 v2.0.2 // indirect
	github.com/juju/schema v1.2.0 // indirect
	github.com/juju/utils/v2 v2.0.0-20210305225158-eedbe7b6b3e2 // indirect
	github.com/juju/version v0.0.0-20210303051006-2015802527a8 // indirect
	github.com/juju/webbrowser v1.0.0 // indirect
	github.com/julienschmidt/httprouter v1.3.0 // indirect
	github.com/k-sone/critbitgo v1.4.0 // indirect
	github.com/klauspost/compress v1.17.11 // indirect
	github.com/klauspost/cpuid/v2 v2.2.9 // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/magiconair/properties v1.8.9 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-runewidth v0.0.16 // indirect
	github.com/mdlayher/socket v0.4.1 // indirect
	github.com/minio/md5-simd v1.1.2 // indirect
	github.com/minio/sha256-simd v1.0.1 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/muhlemmer/gu v0.3.1 // indirect
	github.com/pelletier/go-toml/v2 v2.2.3 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/prometheus/client_model v0.4.0 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/rogpeppe/fastuuid v1.2.0 // indirect
	github.com/rs/xid v1.6.0 // indirect
	github.com/russross/blackfriday/v2 v2.1.0 // indirect
	github.com/sagikazarmark/locafero v0.6.0 // indirect
	github.com/sagikazarmark/slog-shim v0.1.0 // indirect
	github.com/sourcegraph/conc v0.3.0 // indirect
	github.com/spf13/afero v1.11.0 // indirect
	github.com/spf13/cast v1.7.0 // indirect
	github.com/spf13/pflag v1.0.6 // indirect
	github.com/spf13/viper v1.19.0 // indirect
	github.com/subosito/gotenv v1.6.0 // indirect
	github.com/vishvananda/netlink v1.3.0 // indirect
	github.com/vishvananda/netns v0.0.5 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/exp v0.0.0-20241204233417-43b7b7cde48d // indirect
	golang.org/x/mod v0.22.0 // indirect
	golang.org/x/net v0.33.0 // indirect
	golang.org/x/tools v0.28.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20241206012308-a4fef0638583 // indirect
	google.golang.org/grpc v1.68.1 // indirect
	gopkg.in/errgo.v1 v1.0.1 // indirect
	gopkg.in/go-jose/go-jose.v2 v2.6.3 // indirect
	gopkg.in/httprequest.v1 v1.2.1 // indirect
	gopkg.in/ini.v1 v1.67.0 // indirect
	gopkg.in/macaroon.v2 v2.1.0 // indirect
	gopkg.in/mgo.v2 v2.0.0-20190816093944-a6b53ec6cb22 // indirect
	gopkg.in/retry.v1 v1.0.3 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
