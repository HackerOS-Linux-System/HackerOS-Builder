module github.com/HackerOS-Linux-System/hackeros-builder

go 1.22

require github.com/google/go-containerregistry v0.20.2

require (
	github.com/containerd/stargz-snapshotter/estargz v0.14.3 // indirect
	github.com/docker/cli v27.1.1+incompatible // indirect
	github.com/docker/distribution v2.8.2+incompatible // indirect
	github.com/docker/docker-credential-helpers v0.7.0 // indirect
	github.com/klauspost/compress v1.16.5 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.1.0-rc3 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/sirupsen/logrus v1.9.1 // indirect
	github.com/vbatts/tar-split v0.11.3 // indirect
	golang.org/x/sync v0.2.0 // indirect
	golang.org/x/sys v0.15.0 // indirect
)

// --- Replace na potrzeby offline/vendorowanego builda ---
// golang.org i gopkg.in bywaja niedostepne za niektorymi firewallami/
// proxy (m.in. w srodowisku w ktorym te zaleznosci zostaly zvendorowane
// dla tego repo). Ponizsze "replace" wskazuja DOKLADNIE TE SAME wersje
// kodu co oryginalne moduly (te same commity, publikowane rownolegle na
// github.com przez utrzymujacych/mirror), tylko pod adresem git
// osiagalnym z github.com. Poniewaz cala ta zaleznosc jest i tak w
// calosci dostarczona w vendor/ (patrz Makefile: "make build" uzywa
// -mod=vendor), te "replace" maja znaczenie WYLACZNIE przy regenerowaniu
// vendor/ (`make tidy && make vendor-sync`) -- normalny "make build"
// (domyslny cel) NIGDY nie laczy sie z siecia i ich nie potrzebuje.
replace golang.org/x/sys => github.com/golang/sys v0.15.0

replace golang.org/x/sync => github.com/golang/sync v0.3.0

replace gopkg.in/yaml.v2 => github.com/go-yaml/yaml v0.0.0-20170812160011-eb3733d160e7

replace gopkg.in/yaml.v3 => github.com/go-yaml/yaml v0.0.0-20220521103104-8f96da9f5d5e

replace gopkg.in/check.v1 => github.com/go-check/check v0.0.0-20180628173108-788fd7840127

replace gotest.tools/v3 => github.com/gotestyourself/gotest.tools/v3 v3.0.3
