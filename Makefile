
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

.PHONY: all build clean install uninstall fmt lint test docker dockerx86 iso

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

check:
	@test -z $(shell gofmt -l main.go | tee /dev/stderr) || echo "[WARN] Fix formatting issues with 'make fmt'"
	@go vet ./...

run: install
	@$(TARGET)