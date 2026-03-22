
SHELL := /bin/sh

TARGET := booty
.DEFAULT_GOAL: $(TARGET)

VERSION := 0.0.0
BUILD := `git rev-parse HEAD`

TARGETOS=linux
TARGETARCH ?= $(shell go env GOARCH)

LDFLAGS=-ldflags "-s -w -X=main.Version=$(VERSION) -X=main.Build=$(BUILD) -extldflags -static"

SRC = $(shell find . -type f -name '*.go' -not -path "./vendor/*")

DOCKERTAG ?= $(VERSION)
REPOSITORY = ghcr.io/telekom/booty

.PHONY: all build build-all clean install uninstall fmt lint test docker dockerx86 iso slim micro gobgp gobgp-iso dockerx86slim dockerx86micro dockerx86gobgp arm64 arm64-slim arm64-gobgp test-iso getramdisk getramdisk-arm64 test-kvm clab-up clab-down test-e2e-integration clab-boot-up clab-boot-down test-e2e-boot booty-vrnetlab-image clab-vrnetlab-up clab-vrnetlab-down test-e2e-vrnetlab booty-gobgp-test-image clab-gobgp-up clab-gobgp-down test-e2e-gobgp clab-gobgp-vrnetlab-up clab-gobgp-vrnetlab-down test-e2e-gobgp-vrnetlab clab-dhcp-up clab-dhcp-down test-e2e-dhcp clab-bond-up clab-bond-down test-e2e-bond clab-lacp-up clab-lacp-down test-e2e-lacp clab-static-up clab-static-down test-e2e-static clab-multi-nic-up clab-multi-nic-down test-e2e-multi-nic

all: lint test install

$(TARGET): $(SRC)
	@GOOS=$(TARGETOS) GOARCH=$(TARGETARCH) go build $(LDFLAGS) -o $(TARGET)

build: $(TARGET)
	@true

build-all: $(SRC)
	@mkdir -p dist/amd64 dist/arm64
	@GOOS=$(TARGETOS) GOARCH=amd64 go build $(LDFLAGS) -o dist/amd64/$(TARGET)
	@GOOS=$(TARGETOS) GOARCH=arm64 go build $(LDFLAGS) -o dist/arm64/$(TARGET)

clean:
	@rm -f $(TARGET)
	@rm -rf dist

install:
	@echo Building and Installing project
	@go install $(LDFLAGS)

uninstall: clean
	@rm -f $$(which ${TARGET})

fmt:
	@gofmt -l -w $(SRC)

lint:
	@golangci-lint run ./...

test:
	@go test -race -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out
	@go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

dockerx86:
	@docker buildx build --platform linux/amd64 --load -t $(REPOSITORY):$(DOCKERTAG) -f initrd.Dockerfile .

docker:
	@docker buildx build --platform linux/amd64,linux/arm64 --push -t $(REPOSITORY):$(DOCKERTAG) -f initrd.Dockerfile .

iso:
	@docker buildx build --platform linux/amd64 --target iso --output type=local,dest=. -f initrd.Dockerfile .
	@echo ISO built: booty.iso

slim:
	@docker buildx build --platform linux/amd64 --target slim --output type=local,dest=. -f initrd.Dockerfile .
	@echo Slim initramfs built: initramfs.cpio.gz

micro:
	@docker buildx build --platform linux/amd64 --target micro --output type=local,dest=. -f initrd.Dockerfile .
	@echo Micro initramfs built: initramfs.cpio.gz

gobgp:
	@docker buildx build --platform linux/amd64 --target gobgp --output type=local,dest=. -f initrd.Dockerfile .
	@echo GoBGP initramfs built: initramfs.cpio.gz

gobgp-iso:
	@docker buildx build --platform linux/amd64 --target gobgp-iso --output type=local,dest=. -f initrd.Dockerfile .
	@echo GoBGP ISO built: booty-gobgp.iso

dockerx86slim:
	@docker buildx build --platform linux/amd64 --target slim --load -t $(REPOSITORY):$(DOCKERTAG)-slim -f initrd.Dockerfile .

dockerx86micro:
	@docker buildx build --platform linux/amd64 --target micro --load -t $(REPOSITORY):$(DOCKERTAG)-micro -f initrd.Dockerfile .

dockerx86gobgp:
	@docker buildx build --platform linux/amd64 --target gobgp --load -t $(REPOSITORY):$(DOCKERTAG)-gobgp -f initrd.Dockerfile .

arm64:
	@docker buildx build --platform linux/arm64 --load -t $(REPOSITORY):$(DOCKERTAG)-arm64 -f initrd.Dockerfile .

arm64-slim:
	@mkdir -p dist/arm64
	@docker buildx build --platform linux/arm64 --target slim --output type=local,dest=dist/arm64 -f initrd.Dockerfile .
	@echo ARM64 slim initramfs built: dist/arm64/initramfs.cpio.gz

arm64-gobgp:
	@mkdir -p dist/arm64
	@docker buildx build --platform linux/arm64 --target gobgp --output type=local,dest=dist/arm64 -f initrd.Dockerfile .
	@echo ARM64 GoBGP initramfs built: dist/arm64/initramfs.cpio.gz

test-iso:
	@echo Verifying ISO hybrid boot record
	@file booty.iso | grep -q "ISO 9660" || (echo "FAIL: not a valid ISO"; exit 1)
	@echo PASS

# This is typically only for quick testing
getramdisk:

	@ID=$$(docker create $(REPOSITORY):$(DOCKERTAG) null); \
	docker cp $$ID:/initramfs.cpio.gz initramfs.cpio.gz ; docker rm $$ID
	@echo Extracted ramdisk

getramdisk-arm64:
	@mkdir -p dist/arm64
	@ID=$$(docker create $(REPOSITORY):$(DOCKERTAG)-arm64 null); \
	docker cp $$ID:/initramfs.cpio.gz dist/arm64/initramfs.cpio.gz ; docker rm $$ID
	@echo Extracted ARM64 ramdisk to dist/arm64/

simplify:
	@gofmt -s -l -w $(SRC)

test-e2e:
	@echo Running E2E tests
	@go test -tags e2e -race -v ./test/e2e/...

test-kvm:
	@echo Running KVM E2E tests (requires QEMU, root, and KVM assets)
	@go test -tags e2e -race -v -timeout 15m ./test/e2e/kvm/...

clab-up:
	@echo Deploying ContainerLab topology
	@cd test/e2e/clab && sudo clab deploy --topo topology.clab.yml

clab-down:
	@echo Destroying ContainerLab topology
	@cd test/e2e/clab && sudo clab destroy --topo topology.clab.yml

test-e2e-integration:
	@echo Running E2E integration tests (requires clab-up)
	@go test -tags e2e_integration -race -v -timeout 120s ./test/e2e/integration/...

booty-test-image:
	@echo Building BOOTy test container image
	@docker build -t booty-test:latest -f test/e2e/clab/booty-test.Dockerfile .

clab-boot-up: booty-test-image
	@echo Deploying boot test topology (includes BOOTy nodes)
	@cd test/e2e/clab && sudo clab deploy --topo topology-boot.clab.yml

clab-boot-down:
	@echo Destroying boot test topology
	@cd test/e2e/clab && sudo clab destroy --topo topology-boot.clab.yml

test-e2e-boot:
	@echo Running BOOTy boot E2E tests (requires clab-boot-up)
	@go test -tags e2e_boot -race -v -timeout 300s ./test/e2e/integration/...

booty-vrnetlab-image:
	@echo Building BOOTy vrnetlab VM image
	@docker build -t booty-vrnetlab:latest -f test/e2e/clab/vrnetlab/Dockerfile .

clab-vrnetlab-up: booty-vrnetlab-image
	@echo Deploying vrnetlab EVPN topology (QEMU VMs + EVPN fabric)
	@cd test/e2e/clab && sudo clab deploy --topo topology-vrnetlab.clab.yml

clab-vrnetlab-down:
	@echo Destroying vrnetlab EVPN topology
	@cd test/e2e/clab && sudo clab destroy --topo topology-vrnetlab.clab.yml

test-e2e-vrnetlab:
	@echo Running vrnetlab EVPN E2E tests (requires clab-vrnetlab-up)
	@go test -tags e2e_vrnetlab -race -v -timeout 600s ./test/e2e/integration/...

# ── GoBGP e2e targets ──────────────────────────────────────────────────────

booty-gobgp-test-image:
	@echo Building BOOTy GoBGP test container image (no FRR)
	@docker build -t booty-gobgp-test:latest -f test/e2e/clab/booty-gobgp-test.Dockerfile .

clab-gobgp-up: booty-gobgp-test-image
	@echo Deploying GoBGP test topology (unnumbered + dual + numbered)
	@cd test/e2e/clab && sudo clab deploy --topo topology-gobgp.clab.yml

clab-gobgp-down:
	@echo Destroying GoBGP test topology
	@cd test/e2e/clab && sudo clab destroy --topo topology-gobgp.clab.yml

test-e2e-gobgp:
	@echo Running GoBGP E2E tests (requires clab-gobgp-up)
	@go test -tags e2e_gobgp -race -v -timeout 300s ./test/e2e/integration/...

clab-gobgp-vrnetlab-up: booty-vrnetlab-image
	@echo Deploying GoBGP vrnetlab topology (QEMU VMs, all PeerModes)
	@cd test/e2e/clab && sudo clab deploy --topo topology-gobgp-vrnetlab.clab.yml

clab-gobgp-vrnetlab-down:
	@echo Destroying GoBGP vrnetlab topology
	@cd test/e2e/clab && sudo clab destroy --topo topology-gobgp-vrnetlab.clab.yml

test-e2e-gobgp-vrnetlab:
	@echo Running GoBGP vrnetlab E2E tests (requires clab-gobgp-vrnetlab-up)
	@go test -tags e2e_gobgp_vrnetlab -race -v -timeout 600s ./test/e2e/integration/...

# ── DHCP lab targets ───────────────────────────────────────────────────────

clab-dhcp-up:
	@echo Deploying DHCP test topology
	@cd test/e2e/clab && sudo clab deploy --topo topology-dhcp.clab.yml

clab-dhcp-down:
	@echo Destroying DHCP test topology
	@cd test/e2e/clab && sudo clab destroy --topo topology-dhcp.clab.yml

test-e2e-dhcp:
	@echo Running DHCP E2E tests (requires clab-dhcp-up)
	@BOOTY_TOPOLOGY=dhcp go test -tags e2e_integration -race -v -timeout 120s ./test/e2e/integration/... -run TestContainerLabTopologySmoke

# ── Bonding (non-LACP) lab targets ───────────────────────────────────────

clab-bond-up:
	@echo Deploying bond-mode (non-LACP) test topology
	@cd test/e2e/clab && sudo clab deploy --topo topology-lacp.clab.yml

clab-bond-down:
	@echo Destroying bond-mode (non-LACP) test topology
	@cd test/e2e/clab && sudo clab destroy --topo topology-lacp.clab.yml

test-e2e-bond:
	@echo Running bond-mode (non-LACP) E2E tests (requires clab-bond-up)
	@BOOTY_TOPOLOGY=bond go test -tags e2e_integration -race -v -timeout 120s ./test/e2e/integration/... -run TestContainerLabTopologySmoke

# Backward-compatible aliases.
clab-lacp-up: clab-bond-up
clab-lacp-down: clab-bond-down
test-e2e-lacp: test-e2e-bond

# ── Static IP lab targets ─────────────────────────────────────────────────

clab-static-up:
	@echo Deploying static IP test topology
	@cd test/e2e/clab && sudo clab deploy --topo topology-static.clab.yml

clab-static-down:
	@echo Destroying static IP test topology
	@cd test/e2e/clab && sudo clab destroy --topo topology-static.clab.yml

test-e2e-static:
	@echo Running static IP E2E tests (requires clab-static-up)
	@BOOTY_TOPOLOGY=static go test -tags e2e_integration -race -v -timeout 120s ./test/e2e/integration/... -run TestContainerLabTopologySmoke

# ── Multi-NIC lab targets ─────────────────────────────────────────────────

clab-multi-nic-up:
	@echo Deploying multi-NIC test topology
	@cd test/e2e/clab && sudo clab deploy --topo topology-multi-nic.clab.yml

clab-multi-nic-down:
	@echo Destroying multi-NIC test topology
	@cd test/e2e/clab && sudo clab destroy --topo topology-multi-nic.clab.yml

test-e2e-multi-nic:
	@echo Running multi-NIC E2E tests (requires clab-multi-nic-up)
	@BOOTY_TOPOLOGY=multi-nic go test -tags e2e_integration -race -v -timeout 120s ./test/e2e/integration/... -run TestContainerLabTopologySmoke

check:
	@test -z $(shell gofmt -l main.go | tee /dev/stderr) || echo "[WARN] Fix formatting issues with 'make fmt'"
	@go vet ./...

run: install
	@$(TARGET)