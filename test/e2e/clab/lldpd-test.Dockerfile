# Leaf of the cable check lab: lldpd shows what a switch learns from
# BOOTy's LLDP frames (lldpcli), like the switches that NSO asks.
FROM alpine:3.24@sha256:3f4d6b32e26703c6082839234f43769d5046b3ac7c910033911d1e71d8cd630a
RUN set -eu; \
    n=0; \
    until apk add --no-cache lldpd iproute2 tcpdump; do \
      n=$((n+1)); \
      if [ "$n" -ge 5 ]; then echo "apk add failed after $n attempts" >&2; exit 1; fi; \
      sleep 10; \
    done
CMD ["sleep", "infinity"]
