# Test container for GoBGP mode inside containerlab.
# Unlike booty-test.Dockerfile, this does NOT install FRR — GoBGP is compiled
# directly into the BOOTy binary and runs in-process.
FROM golang:1.26-alpine AS builder
RUN apk add --no-cache git ca-certificates gcc linux-headers musl-dev
COPY go.mod go.sum /go/src/github.com/telekom/BOOTy/
WORKDIR /go/src/github.com/telekom/BOOTy
RUN go mod download
COPY . /go/src/github.com/telekom/BOOTy/
RUN CGO_ENABLED=1 GOOS=linux go build -a \
    -ldflags "-linkmode external -extldflags '-static' -s -w" \
    -o /booty

FROM alpine:3.23
RUN apk add --no-cache ca-certificates iproute2
# Disk provisioning tools needed for full provisioning pipeline.
RUN apk add --no-cache e2fsprogs dosfstools sgdisk parted lvm2 util-linux
COPY --from=builder /booty /usr/local/bin/booty
RUN mkdir -p /deploy /tmp

CMD ["/bin/sh", "-c", "/usr/local/bin/booty 2>&1; sleep infinity"]
