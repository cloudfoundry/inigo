#!/bin/bash

set -eu
set -o pipefail

function install_dependencies() {
  DEBIAN_FRONTEND=noninteractive apt -qq update
  DEBIAN_FRONTEND=noninteractive apt-get install -yq dnsmasq libseccomp-dev xfsprogs uidmap ulogd2 autoconf pkg-config lsb-release btrfs-progs
}

# Setup DNS for *.service.cf.internal, used by the Diego components, and
# *.test.internal, used by the collocated DUSTs as a routable domain.
function setup_dnsmasq() {
  local host_addr
  host_addr=$(ip route get 8.8.8.8 | head -n1 | awk '{print $7}')

  dnsmasq --address=/service.cf.internal/127.0.0.1 --address=/test.internal/${host_addr}
  echo -e "nameserver $host_addr\n$(cat /etc/resolv.conf)" > /etc/resolv.conf
}

function build_gardenrunc() {
  pushd ${GARDEN_RUNC_RELEASE_PATH}
    export PATH=${PWD}/bin:${PATH}
    export GARDEN_BINPATH=${PWD}/bin/

    mkdir -p ${GARDEN_RUNC_RELEASE_PATH}/bin
    NSTAR_PATH=src/guardian/rundmc/nstar

    gcc -static -o $GARDEN_BINPATH/init $GARDEN_RUNC_RELEASE_PATH/src/guardian/cmd/init/init.c $GARDEN_RUNC_RELEASE_PATH/src/guardian/cmd/init/ignore_sigchild.c
    pushd ./src/guardian
      go build -mod vendor -o "${GARDEN_BINPATH}/dadoo" ./cmd/dadoo
    popd
    pushd ./src/grootfs
      go build -mod vendor -o "${GARDEN_BINPATH}/tardis" ./store/filesystems/overlayxfs/tardis
    popd

    export GOPATH="$PWD/src/gopath"
    pushd src/guardian/vendor/github.com/opencontainers/runc
      PKG_CONFIG_PATH=/usr/local/lib/pkgconfig GOPATH=${PWD}/Godeps/_workspace:${GOPATH} BUILDTAGS="seccomp" make static
      mv runc ${GARDEN_BINPATH}
    popd

    pushd $NSTAR_PATH
      make && cp nstar ${GARDEN_BINPATH}
    popd

    cp "${TAR_BINARY}" "${GARDEN_BINPATH}/tar"

  popd
}

function create_garden_storage() {
  # Configure cgroup
  mount -t tmpfs cgroup_root /sys/fs/cgroup
  mkdir -p /sys/fs/cgroup/devices
  mkdir -p /sys/fs/cgroup/memory

  mount -tcgroup -odevices cgroup:devices /sys/fs/cgroup/devices
  devices_mount_info=$(cat /proc/self/cgroup | grep devices)
  devices_subdir=$(echo $devices_mount_info | cut -d: -f3)

  # change permission to allow us to run mknod later
  echo 'b 7:* rwm' > /sys/fs/cgroup/devices/devices.allow
  echo 'b 7:* rwm' > /sys/fs/cgroup/devices${devices_subdir}/devices.allow

  # Setup loop devices
  for i in {0..256}
  do
    rm -f /dev/loop$i
    mknod -m777 /dev/loop$i b 7 $i
  done

  # Make XFS volume
  truncate -s 8G /xfs_volume
  mkfs.xfs -b size=4096 /xfs_volume

  # Mount XFS
  mkdir /mnt/garden-storage
  mount -t xfs -o pquota,noatime /xfs_volume /mnt/garden-storage
  chmod 777 -R /mnt/garden-storage

  umount /sys/fs/cgroup/devices
}

build_grootfs () {
  echo "Building grootfs..."
  export GROOTFS_BINPATH=${GARDEN_RUNC_RELEASE_PATH}/bin
  mkdir -p ${GROOTFS_BINPATH}

  pushd ${GARDEN_RUNC_RELEASE_PATH}/src/grootfs
    export PATH=${GROOTFS_BINPATH}:${PATH}

    # Set up btrfs volume and loopback devices in environment
    create_garden_storage
    umount /sys/fs/cgroup

    make

    mv $PWD/build/grootfs $GROOTFS_BINPATH
    echo "grootfs installed."

    groupadd iamgroot -g 4294967294
    useradd iamgroot -u 4294967294 -g 4294967294
    echo "iamgroot:1:4294967293" > /etc/subuid
    echo "iamgroot:1:4294967293" > /etc/subgid
  popd
}

build_garden_rootfs() {
  tar cpf /tmp/rootfs.tar -C "$GARDEN_ROOTFS_FILES" .
  export GARDEN_ROOTFS=/tmp/rootfs.tar
}

setup_diego_release() {
  pushd ${DIEGO_RELEASE_PATH}

  export CODE_CLOUDFOUNDRY_ORG_MODULE="$PWD/src/code.cloudfoundry.org"
  export GUARDIAN_MODULE="$PWD/src/guardian"
  popd
}

setup_database() {
  orig_ca_file="${DIEGO_RELEASE_PATH}/src/code.cloudfoundry.org/inigo/fixtures/certs/sql-certs/server-ca.crt"
  orig_cert_file="${DIEGO_RELEASE_PATH}/src/code.cloudfoundry.org/inigo/fixtures/certs/sql-certs/server.crt"
  orig_key_file="${DIEGO_RELEASE_PATH}/src/code.cloudfoundry.org/inigo/fixtures/certs/sql-certs/server.key"

  ca_file="/tmp/server-ca.crt"
  cert_file="/tmp/server.crt"
  key_file="/tmp/server.key"

  # do not chown/chmod files in the inigo repo that is annoying
  cp $orig_ca_file $ca_file
  cp $orig_cert_file $cert_file
  cp $orig_key_file $key_file

  chmod 0600 "$ca_file"
  chmod 0600 "$cert_file"
  chmod 0600 "$key_file"

  if [ "${DB}" = "mysql" ]; then
    pkill mysqld

    chown mysql:mysql "$ca_file"
    chown mysql:mysql "$cert_file"
    chown mysql:mysql "$key_file"

    local server_cnf_path="/etc/mysql/conf.d/docker.cnf"
    sed -i 's/# max_connections.*= 151/max_connections = 2000/g' "${server_cnf_path}"
    echo "ssl-cert = $cert_file" >> "${server_cnf_path}"
    echo "ssl-key = $key_file" >> "${server_cnf_path}"
    echo "ssl-ca = $ca_file" >> "${server_cnf_path}"

  cat << EOF > /etc/my.cnf
[mysqld]
sql_mode=NO_ENGINE_SUBSTITUTION,STRICT_TRANS_TABLES
EOF
  local datadir=/mysql-datadir
  local escaped_datadir=${datadir/\//\\\/}
  mkdir $datadir
  mount -t tmpfs -o size=2g tmpfs $datadir
  rsync -av --progress /var/lib/mysql/ $datadir
  sed -i "s/#datadir.*/datadir=${escaped_datadir}/g" "${server_cnf_path}"

  else
    local pg_conf=$(ls /etc/postgresql/*/main/postgresql.conf)
    sed -i 's/max_connections = 100/max_connections = 2000/g' "${pg_conf}"

    chown postgres:postgres "$ca_file"
    chown postgres:postgres "$cert_file"
    chown postgres:postgres "$key_file"


    sed -i 's/ssl = false/ssl = true/g' "${pg_conf}"
    sed -i "s%ssl_cert_file = '/etc/ssl/certs/ssl-cert-snakeoil.pem'%ssl_cert_file = '$cert_file'%g" "${pg_conf}"
    sed -i "s%ssl_key_file = '/etc/ssl/private/ssl-cert-snakeoil.key'%ssl_key_file = '$key_file'%g" "${pg_conf}"
    sed -i "s%#ssl_ca_file = ''%ssl_ca_file = '$ca_file'%g" "${pg_conf}"
  fi

  configure_db "${DB}"
}

# Update to concourse 2.7.3 and garden-runc 1.4.0 caused inigo to fail since
# resolv.conf is now bind-mounted in. Removing the apt-get install, two inigo
# tests were failing because they were unable to resolve DNS names.
echo "nameserver 8.8.8.8" >> /etc/resolv.conf

install_dependencies
setup_dnsmasq
build_gardenrunc
build_grootfs
build_garden_rootfs

export ROUTER_GOPATH="$ROUTING_RELEASE_PATH/src/code.cloudfoundry.org"
export ROUTING_API_GOPATH=${ROUTER_GOPATH}

setup_diego_release

export APP_LIFECYCLE_GOPATH=${CODE_CLOUDFOUNDRY_ORG_MODULE}
export AUCTIONEER_GOPATH=${CODE_CLOUDFOUNDRY_ORG_MODULE}
export BBS_GOPATH=${CODE_CLOUDFOUNDRY_ORG_MODULE}
export FILE_SERVER_GOPATH=${CODE_CLOUDFOUNDRY_ORG_MODULE}
export HEALTHCHECK_GOPATH=${CODE_CLOUDFOUNDRY_ORG_MODULE}
export LOCKET_GOPATH=${CODE_CLOUDFOUNDRY_ORG_MODULE}
export REP_GOPATH=${CODE_CLOUDFOUNDRY_ORG_MODULE}
export ROUTE_EMITTER_GOPATH=${CODE_CLOUDFOUNDRY_ORG_MODULE}
export SSHD_GOPATH=${CODE_CLOUDFOUNDRY_ORG_MODULE}
export SSH_PROXY_GOPATH=${CODE_CLOUDFOUNDRY_ORG_MODULE}
export GARDEN_GOPATH=${GUARDIAN_MODULE}

# used for routing to apps; same logic that Garden uses.
EXTERNAL_ADDRESS=$(ip route get 8.8.8.8 | sed 's/.*src\s\(.*\)\suid.*/\1/;tx;d;:x')
export EXTERNAL_ADDRESS

setup_database

# display ginkgo dots properly
export LESSCHARSET=utf-8

export GARDEN_GRAPH_PATH=/tmp/aufs_mount
mkdir -p "${GARDEN_GRAPH_PATH}"
mount -t tmpfs tmpfs "${GARDEN_GRAPH_PATH}"

# workaround until Concourse's garden sets this up for us
if ! grep -qs '/sys' /proc/mounts; then
  mount -t sysfs sysfs /sys
fi

# shellcheck source=/dev/null
source "${GARDEN_RUNC_RELEASE_PATH}/ci/helpers/device-control.bash"
permit_device_control
create_loop_devices 256

# shellcheck disable=SC2068
# Double-quoting array expansion here causes ginkgo to fail
go run github.com/onsi/ginkgo/v2/ginkgo ${@}
