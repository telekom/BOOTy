# Never built; see ../Dockerfile. The Type-5 lab (topology-type5.clab.yml)
# needs FRR 10.3.2 or later, the first release with the encapsulation relay
# fix, while the other labs run ../Dockerfile's FRR. In one directory
# Dependabot would merge both FRR lines into one dependency, so this one is
# a Dependabot directory of its own. TestType5TopologyUsesFRRWithType5EncapRelayFix
# rejects versions below 10.3.2.

FROM quay.io/frrouting/frr:10.3.2@sha256:ada2b4c4407f9dabcca0bbf35aa32344d21de2adc5606e6df022c45032d95203 AS frr-type5
