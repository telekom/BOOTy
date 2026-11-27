module github.com/telekom/BOOTy

go 1.27.4

require (
	github.com/GehirnInc/crypt v0.0.0-20230320061759-8cc1b52080c5
	github.com/dustin/go-humanize v1.1.0
	github.com/fsnotify/fsnotify v1.10.1
	github.com/google/go-containerregistry v0.22.1
	github.com/google/go-tpm v0.9.8
	github.com/gopacket/gopacket v1.7.4
	github.com/insomniacslk/dhcp v0.0.0-20260901064844-234b97448fae
	github.com/klauspost/compress v1.20.5
	github.com/mdlayher/packet v1.2.0
	github.com/openpubkey/openpubkey v0.30.1
	github.com/openpubkey/opkssh v0.20.0
	github.com/openwall/yescrypt-go v1.0.0
	github.com/osrg/gobgp/v4 v4.11.0
	github.com/pierrec/lz4/v4 v4.1.34
	github.com/spf13/cobra v1.10.2
	github.com/telekom/t-caas-go-library v0.1.8
	github.com/ulikunitz/xz v0.5.17
	github.com/vishvananda/netlink v1.3.1
	github.com/vishvananda/netns v0.0.5
	github.com/zcalusic/sysinfo v1.2.3
	golang.org/x/crypto v0.59.0
	golang.org/x/net v0.63.0
	golang.org/x/sys v0.50.0
	google.golang.org/protobuf v1.36.12
	gopkg.in/yaml.v3 v3.0.1
)

require (
	filippo.io/bigmod v0.1.0 // indirect
	github.com/awnumar/memcall v0.5.0 // indirect
	github.com/awnumar/memguard v0.23.0 // indirect
	github.com/bits-and-blooms/bitset v1.30.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.4.1 // indirect
	github.com/dgryski/go-farm v0.0.0-20240924180020-3414d57e47da // indirect
	github.com/docker/cli v29.10.1+incompatible // indirect
	github.com/docker/docker-credential-helpers v0.9.13 // indirect
	github.com/eapache/channels v1.1.0 // indirect
	github.com/eapache/queue v1.1.0 // indirect
	github.com/gaissmai/bart v0.30.0 // indirect
	github.com/go-jose/go-jose/v4 v4.1.9 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/goccy/go-json v0.12.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/securecookie v1.1.2 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/k-sone/critbitgo v1.4.0 // indirect
	github.com/lestrrat-go/blackmagic v1.0.4 // indirect
	github.com/lestrrat-go/dsig v1.4.0 // indirect
	github.com/lestrrat-go/dsig-secp256k1 v1.0.0 // indirect
	github.com/lestrrat-go/httpcc v1.0.1 // indirect
	github.com/lestrrat-go/httprc/v3 v3.0.6 // indirect
	github.com/lestrrat-go/jwx/v3 v3.4.0 // indirect
	github.com/lestrrat-go/option/v2 v2.0.0 // indirect
	github.com/mdlayher/socket v0.8.0 // indirect
	github.com/muhlemmer/gu v0.3.1 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.1.1 // indirect
	github.com/orcaman/concurrent-map/v2 v2.0.1 // indirect
	github.com/pelletier/go-toml/v2 v2.4.3 // indirect
	github.com/sagikazarmark/locafero v0.12.0 // indirect
	github.com/segmentio/asm v1.2.1 // indirect
	github.com/segmentio/fasthash v1.0.3 // indirect
	github.com/sirupsen/logrus v1.10.2 // indirect
	github.com/spf13/afero v1.15.0 // indirect
	github.com/spf13/cast v1.10.0 // indirect
	github.com/spf13/pflag v1.1.0 // indirect
	github.com/spf13/viper v1.21.0 // indirect
	github.com/stretchr/testify v1.12.1 // indirect
	github.com/subosito/gotenv v1.6.0 // indirect
	github.com/u-root/uio v0.0.0-20240224005618-d2acac8f3701 // indirect
	github.com/valyala/fastjson v1.6.10 // indirect
	github.com/yeqown/go-qrcode/v2 v2.3.0 // indirect
	github.com/yeqown/reedsolomon v1.0.0 // indirect
	github.com/zitadel/oidc/v3 v3.53.0 // indirect
	github.com/zitadel/schema v1.3.2 // indirect
	go.opentelemetry.io/auto/sdk v1.2.2 // indirect
	go.opentelemetry.io/otel v1.47.0 // indirect
	go.opentelemetry.io/otel/log v1.47.0 // indirect
	go.opentelemetry.io/otel/metric v1.47.0 // indirect
	go.opentelemetry.io/otel/trace v1.47.0 // indirect
	go.yaml.in/yaml/v3 v3.0.6 // indirect
	golang.org/x/exp v0.0.0-20250717185816-542afb5b7346 // indirect
	golang.org/x/oauth2 v0.38.0 // indirect
	golang.org/x/sync v0.25.0 // indirect
	golang.org/x/text v0.44.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20261110000943-09e21b237f2c // indirect
	google.golang.org/grpc v1.85.0 // indirect
	gotest.tools/v3 v3.5.2 // indirect
)

// GoBGP master after 4.11.0 (629e5ae8) plus the BOOTy fixes; why, and when
// to drop it:
// docs/networking.md#gobgp-version-and-fork
replace github.com/osrg/gobgp/v4 => github.com/MaxRink/gobgp/v4 v4.11.0-booty.6

// gopacket 1.7.4 and netlink 1.3.1 plus the BOOTy fixes; why, and when to
// drop them: docs/dependency-forks.md
replace github.com/gopacket/gopacket => github.com/MaxRink/gopacket v1.7.4-booty.2

replace github.com/vishvananda/netlink => github.com/MaxRink/netlink v1.3.1-booty.1

// insomniacslk/dhcp 234b97448fae plus the BOOTy fixes; why, and when to
// drop it: docs/dependency-forks.md
replace github.com/insomniacslk/dhcp => github.com/MaxRink/dhcp v0.0.0-20260901064844-234b97448fae-booty.2
