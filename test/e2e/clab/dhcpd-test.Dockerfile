# DHCP server image for the containerlab DHCP topology.
# The upstream networkboot/dhcpd image ships ISC dhcpd but no iproute2.
# Containerlab needs "ip" inside the node to address the data plane link.
FROM networkboot/dhcpd:1.3.0
RUN apt-get -o Acquire::Retries=5 update \
    && apt-get -o Acquire::Retries=5 install -y --no-install-recommends iproute2 procps \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /var/lib/dhcp \
    && touch /var/lib/dhcp/dhcpd.leases
