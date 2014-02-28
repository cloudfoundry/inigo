# this builds the base image for Garden's CI

FROM mischief/docker-golang

# generate locales to shut perl up
RUN locale-gen en_US.UTF-8

# pull in dependencies for the Garden server
RUN apt-get -y install iptables quota rsync net-tools protobuf-compiler zip

# pull in the prebuilt rootfs
ADD inigo-test-rootfs.tar /opt/warden/rootfs

# install the binary for generating the protocol
RUN go get code.google.com/p/gogoprotobuf/protoc-gen-gogo
