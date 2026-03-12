# Test container that runs BOOTy in CAPRF mode inside containerlab.
# BOOTy is built from source and runs as a regular process (not PID 1 init).
# Mount/device setup will fail gracefully. It will reach CAPRF, report init,
# then fail at disk operations (expected in container context).
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
# Install FRR from the official Alpine repo for EVPN networking support.
RUN apk add --no-cache frr
COPY --from=builder /booty /usr/local/bin/booty
RUN mkdir -p /deploy /tmp /etc/frr /var/run/frr && \
    chown -R frr:frr /etc/frr /var/run/frr

# Entrypoint: run BOOTy in CAPRF mode.
# BOOTy writes structured logs to stderr; direct output avoids pipe buffering.
CMD ["/bin/sh", "-c", "/usr/local/bin/booty 2>&1; sleep infinity"]

