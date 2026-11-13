# Leaf of the cable check lab: lldpd shows what a switch learns from
# BOOTy's LLDP frames (lldpcli), like the switches that NSO asks.
FROM alpine:3.24@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf
RUN set -eu; \
    n=0; \
    until apk add --no-cache lldpd iproute2 tcpdump; do \
      n=$((n+1)); \
      if [ "$n" -ge 5 ]; then echo "apk add failed after $n attempts" >&2; exit 1; fi; \
      sleep 10; \
    done
CMD ["sleep", "infinity"]
