# Never built. This file only exists so Dependabot tracks the external images
# of the containerlab topologies (test/e2e/clab/*.clab.yml) and the FRR/Go
# images of test/e2e/bfd/frr-interop.sh, which Dependabot cannot parse.
#
# Stage <name> becomes BOOTY_LAB_<NAME>_IMAGE (dashes as underscores), which
# the topologies use as image: ${BOOTY_LAB_<NAME>_IMAGE}. Shell callers get the
# variables from test/e2e/clab/lab-images.sh, Go tests from package labimages.
#
# This is a directory of its own because Dependabot merges FROM lines with the
# same image name across all Dockerfiles of a directory into one dependency:
# next to test/e2e/clab/*.Dockerfile, alpine:3.20 here would merge with their
# alpine:3.24 and golang:1.27.4 with golang:1.27.4-alpine. The Type-5 FRR
# image (10.3.2) is in frr-type5/Dockerfile for the same reason.
#
# FRR updates other than patch and digest updates are ignored in
# .github/dependabot.yml (CONTRIBUTING.md).

FROM quay.io/frrouting/frr:10.3.1@sha256:f90d26a9fd5c14fc5795a73b4254ac88bc3186c45bbeb220a225fb6182de812c AS frr
FROM nginx:1.31.8-alpine@sha256:fed7e14be83cbd2661a75cdc5b872d936cf155d46c327084d30b8df19ae88d72 AS nginx
FROM alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc AS alpine
FROM ghcr.io/srl-labs/network-multitool:v0.5.0@sha256:ecfbd74784a490a2aec688e63a2224c1c8625b0e0d9e5d9fcd19217647089067 AS multitool
# test/e2e/bfd/frr-interop.sh runs the BFD interop test in this image.
FROM golang:1.27.4@sha256:5e8afd493a7124a8586e134d9b532731161d4b9becf9dee5a5956b107426f93d AS golang
