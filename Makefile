
SHELL := /bin/sh

TARGET := booty
.DEFAULT_GOAL: $(TARGET)

VERSION := 0.0.0
BUILD := `git rev-parse HEAD`

TARGETOS=linux

LDFLAGS=-ldflags "-s -w -X=main.Version=$(VERSION) -X=main.Build=$(BUILD) -extldflags -static"

SRC = $(shell find . -type f -name '*.go' -not -path "./vendor/*")

DOCKERTAG ?= $(VERSION)
REPOSITORY = ghcr.io/telekom/booty

.PHONY: all build clean install uninstall fmt lint test docker dockerx86 iso slim micro dockerx86slim dockerx86micro clab-up clab-down test-e2e-integration clab-boot-up clab-boot-down test-e2e-boot booty-vrnetlab-image clab-vrnetlab-up clab-vrnetlab-down test-e2e-vrnetlab

all: lint test install

$(TARGET): $(SRC)
	@go build $(LDFLAGS) -o $(TARGET)

build: $(TARGET)
	@true

clean:
	@rm -f $(TARGET)

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

dockerx86slim:
	@docker buildx build --platform linux/amd64 --target slim --load -t $(REPOSITORY):$(DOCKERTAG)-slim -f initrd.Dockerfile .

dockerx86micro:
	@docker buildx build --platform linux/amd64 --target micro --load -t $(REPOSITORY):$(DOCKERTAG)-micro -f initrd.Dockerfile .

test-iso:
	@echo Verifying ISO hybrid boot record
	@file booty.iso | grep -q "ISO 9660" || (echo "FAIL: not a valid ISO"; exit 1)
	@echo PASS

# This is typically only for quick testing
getramdisk:

	@ID=$$(docker create $(REPOSITORY)/$(TARGET):$(DOCKERTAG) null); \
	docker cp $$ID:/initramfs.cpio.gz initramfs.cpio.gz ; docker rm $$ID
	@echo Extracted ramdisk

simplify:
	@gofmt -s -l -w $(SRC)

test-e2e:
	@echo Running E2E tests
	@go test -tags e2e -race -v ./test/e2e/...

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

check:
	@test -z $(shell gofmt -l main.go | tee /dev/stderr) || echo "[WARN] Fix formatting issues with 'make fmt'"
	@go vet ./...

run: install
	@$(TARGET)