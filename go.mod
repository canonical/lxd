module github.com/lxc/lxd

go 1.18

replace google.golang.org/grpc/naming => google.golang.org/grpc v1.29.1

require (
	github.com/Rican7/retry v0.3.1
	github.com/armon/go-proxyproto v0.0.0-20210323213023-7e956b284f0a
	github.com/canonical/candid v1.12.1
	github.com/canonical/go-dqlite v1.11.2
	github.com/digitalocean/go-qemu v0.0.0-20220804221245-2002801203aa
	github.com/digitalocean/go-smbios v0.0.0-20180907143718-390a4f403a8e
	github.com/dustinkirkland/golang-petname v0.0.0-20191129215211-8e5a1ed0cff0
	github.com/flosch/pongo2 v0.0.0-20200913210552-0d938eb266f3
	github.com/fsnotify/fsnotify v1.5.4
	github.com/fvbommel/sortorder v1.0.2
	github.com/google/gopacket v1.1.19
	github.com/gorilla/mux v1.8.0
	github.com/gorilla/websocket v1.5.0
	github.com/gosexy/gettext v0.0.0-20160830220431-74466a0a0c4a
	github.com/j-keck/arping v1.0.2
	github.com/jaypipes/pcidb v1.0.0
	github.com/jochenvg/go-udev v0.0.0-20171110120927-d6b62d56d37b
	github.com/juju/gomaasapi v0.0.0-20200602032615-aa561369c767
	github.com/juju/persistent-cookiejar v1.0.0
	github.com/kballard/go-shellquote v0.0.0-20180428030007-95032a82bc51
	github.com/lxc/go-lxc v0.0.0-20220627182551-ad3d9f7cb822
	github.com/mattn/go-colorable v0.1.13
	github.com/mattn/go-sqlite3 v1.14.15
	github.com/mdlayher/ndp v0.10.0
	github.com/mdlayher/netx v0.0.0-20220422152302-c711c2f8512f
	github.com/mdlayher/vsock v1.1.1
	github.com/miekg/dns v1.1.50
	github.com/olekukonko/tablewriter v0.0.5
	github.com/osrg/gobgp/v3 v3.5.0
	github.com/pborman/uuid v1.2.1
	github.com/pkg/sftp v1.13.5
	github.com/robfig/cron/v3 v3.0.1
	github.com/sirupsen/logrus v1.9.0
	github.com/spf13/cobra v1.5.0
	github.com/stretchr/testify v1.8.0
	github.com/syndtr/gocapability v0.0.0-20200815063812-42c35b437635
	golang.org/x/crypto v0.0.0-20220817201139-bc19a97f63c8
	golang.org/x/sys v0.0.0-20220818161305-2296e01440c6
	golang.org/x/term v0.0.0-20220722155259-a9ba230a4035
	golang.org/x/text v0.3.7
	google.golang.org/protobuf v1.28.1
	gopkg.in/juju/environschema.v1 v1.0.0
	gopkg.in/macaroon-bakery.v3 v3.0.0
	gopkg.in/tomb.v2 v2.0.0-20161208151619-d5d1b5820637
	gopkg.in/yaml.v2 v2.4.0
)

require (
	github.com/cpuguy83/go-md2man/v2 v2.0.2 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/dgryski/go-farm v0.0.0-20200201041132-a6ae2369ad13 // indirect
	github.com/digitalocean/go-libvirt v0.0.0-20220811165305-15feff002086 // indirect
	github.com/eapache/channels v1.1.0 // indirect
	github.com/eapache/queue v1.1.0 // indirect
	github.com/ghodss/yaml v1.0.0 // indirect
	github.com/go-macaroon-bakery/macaroonpb v1.0.0 // indirect
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/google/renameio v1.0.1 // indirect
	github.com/google/uuid v1.3.0 // indirect
	github.com/hashicorp/hcl v1.0.0 // indirect
	github.com/inconshreveable/mousetrap v1.0.1 // indirect
	github.com/jkeiser/iter v0.0.0-20200628201005-c8aa0ae784d1 // indirect
	github.com/juju/collections v1.0.0 // indirect
	github.com/juju/errors v1.0.0 // indirect
	github.com/juju/go4 v0.0.0-20160222163258-40d72ab9641a // indirect
	github.com/juju/loggo v1.0.0 // indirect
	github.com/juju/mgo/v2 v2.0.2 // indirect
	github.com/juju/schema v1.0.1 // indirect
	github.com/juju/utils/v2 v2.0.0-20210305225158-eedbe7b6b3e2 // indirect
	github.com/juju/version v0.0.0-20210303051006-2015802527a8 // indirect
	github.com/juju/webbrowser v1.0.0 // indirect
	github.com/julienschmidt/httprouter v1.3.0 // indirect
	github.com/k-sone/critbitgo v1.4.0 // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/magiconair/properties v1.8.6 // indirect
	github.com/mattn/go-isatty v0.0.16 // indirect
	github.com/mattn/go-runewidth v0.0.13 // indirect
	github.com/mdlayher/socket v0.2.3 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/pelletier/go-toml v1.9.5 // indirect
	github.com/pelletier/go-toml/v2 v2.0.3 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/rivo/uniseg v0.3.4 // indirect
	github.com/rogpeppe/fastuuid v1.2.0 // indirect
	github.com/russross/blackfriday/v2 v2.1.0 // indirect
	github.com/spf13/afero v1.9.2 // indirect
	github.com/spf13/cast v1.5.0 // indirect
	github.com/spf13/jwalterweatherman v1.1.0 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/spf13/viper v1.12.0 // indirect
	github.com/subosito/gotenv v1.4.0 // indirect
	github.com/vishvananda/netlink v1.2.1-beta.2 // indirect
	github.com/vishvananda/netns v0.0.0-20211101163701-50045581ed74 // indirect
	gitlab.com/golang-commonmark/puny v0.0.0-20191124015043-9f83538fa04f // indirect
	golang.org/x/mod v0.6.0-dev.0.20220419223038-86c51ed26bb4 // indirect
	golang.org/x/net v0.0.0-20220812174116-3211cb980234 // indirect
	golang.org/x/sync v0.0.0-20220819030929-7fc1605a5dde // indirect
	golang.org/x/tools v0.1.12 // indirect
	google.golang.org/genproto v0.0.0-20220819174105-e9f053255caa // indirect
	google.golang.org/grpc v1.48.0 // indirect
	google.golang.org/grpc/naming v0.0.0-00010101000000-000000000000 // indirect
	gopkg.in/errgo.v1 v1.0.1 // indirect
	gopkg.in/httprequest.v1 v1.2.1 // indirect
	gopkg.in/ini.v1 v1.67.0 // indirect
	gopkg.in/macaroon.v2 v2.1.0 // indirect
	gopkg.in/mgo.v2 v2.0.0-20190816093944-a6b53ec6cb22 // indirect
	gopkg.in/retry.v1 v1.0.3 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
