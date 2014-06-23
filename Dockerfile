# this builds the base image for Garden's CI

FROM ubuntu:14.04

# env vars
ENV HOME /root
ENV GOPATH /root/go
ENV PATH /root/go/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/usr/games

# GOPATH
RUN mkdir -p /root/go

# apt
RUN echo "deb http://mirror.anl.gov/pub/ubuntu trusty main universe" > /etc/apt/sources.list
RUN apt-get update
RUN apt-get install -y build-essential mercurial git-core subversion wget

# go 1.3 tarball
RUN wget -qO- http://golang.org/dl/go1.3.linux-amd64.tar.gz | tar -C /usr/local -xzf -

# generate locales to shut perl up
RUN locale-gen en_US.UTF-8

# pull in dependencies for the Garden server
RUN apt-get -y install iptables quota rsync net-tools protobuf-compiler zip

# pull in the prebuilt rootfs
ADD inigo-test-rootfs.tar /opt/warden/rootfs

# install the binary for generating the protocol
RUN go get code.google.com/p/gogoprotobuf/protoc-gen-gogo
