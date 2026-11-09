# DHCP server image for the containerlab DHCP topology.
# The upstream networkboot/dhcpd image ships ISC dhcpd but no iproute2.
# Containerlab needs "ip" inside the node to address the data plane link.
FROM networkboot/dhcpd:1.3.0@sha256:706b245c982689416d6d74f267da28232a0e99b74bac7e1d8bb050eeca473a4d
RUN apt-get -o Acquire::Retries=5 update \
    && apt-get -o Acquire::Retries=5 install -y --no-install-recommends iproute2 procps \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /var/lib/dhcp \
    && touch /var/lib/dhcp/dhcpd.leases
