package helpers

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"code.cloudfoundry.org/bbs/test_helpers"
	"code.cloudfoundry.org/diego-ssh/keys"
	"code.cloudfoundry.org/inigo/helpers/portauthority"
	"code.cloudfoundry.org/inigo/world"
	repconfig "code.cloudfoundry.org/rep/cmd/rep/config"
	"github.com/nu7hatch/gouuid"

	"github.com/onsi/ginkgo"

	. "github.com/onsi/gomega"
)

var PreloadedStacks = []string{"red-stack", "blue-stack", "cflinuxfs2"}
var DefaultStack = PreloadedStacks[0]

const AssetsPath = "../fixtures/certs/"

func DBInfo() (string, string) {
	var dbBaseConnectionString string
	var dbDriverName string

	if test_helpers.UsePostgres() {
		dbDriverName = "postgres"
		dbBaseConnectionString = "postgres://diego:diego_pw@127.0.0.1/"
	} else {
		dbDriverName = "mysql"
		dbBaseConnectionString = "diego:diego_password@tcp(localhost:3306)/"
	}

	return dbDriverName, dbBaseConnectionString
}

func MakeComponentMaker(assetsPath string, builtArtifacts world.BuiltArtifacts, worldAddresses world.ComponentAddresses) world.ComponentMaker {
	grootfsBinPath := os.Getenv("GROOTFS_BINPATH")
	gardenBinPath := os.Getenv("GARDEN_BINPATH")
	gardenRootFSPath := os.Getenv("GARDEN_ROOTFS")
	gardenGraphPath := os.Getenv("GARDEN_GRAPH_PATH")
	externalAddress := os.Getenv("EXTERNAL_ADDRESS")

	if gardenGraphPath == "" {
		gardenGraphPath = os.TempDir()
	}

	if world.UseGrootFS() {
		Expect(grootfsBinPath).NotTo(BeEmpty(), "must provide $GROOTFS_BINPATH")
	}
	Expect(gardenBinPath).NotTo(BeEmpty(), "must provide $GARDEN_BINPATH")
	Expect(gardenRootFSPath).NotTo(BeEmpty(), "must provide $GARDEN_ROOTFS")
	Expect(externalAddress).NotTo(BeEmpty(), "must provide $EXTERNAL_ADDRESS")

	stackPathMap := make(repconfig.RootFSes, len(PreloadedStacks))
	for i, stack := range PreloadedStacks {
		stackPathMap[i] = repconfig.RootFS{
			Name: stack,
			Path: gardenRootFSPath,
		}
	}

	hostKeyPair, err := keys.RSAKeyPairFactory.NewKeyPair(1024)
	Expect(err).NotTo(HaveOccurred())

	userKeyPair, err := keys.RSAKeyPairFactory.NewKeyPair(1024)
	Expect(err).NotTo(HaveOccurred())

	sshKeys := world.SSHKeys{
		HostKey:       hostKeyPair.PrivateKey(),
		HostKeyPem:    hostKeyPair.PEMEncodedPrivateKey(),
		PrivateKeyPem: userKeyPair.PEMEncodedPrivateKey(),
		AuthorizedKey: userKeyPair.AuthorizedKey(),
	}
	bbsServerCert, err := filepath.Abs(assetsPath + "bbs_server.crt")
	Expect(err).NotTo(HaveOccurred())
	bbsServerKey, err := filepath.Abs(assetsPath + "bbs_server.key")
	Expect(err).NotTo(HaveOccurred())
	repServerCert, err := filepath.Abs(assetsPath + "rep_server.crt")
	Expect(err).NotTo(HaveOccurred())
	repServerKey, err := filepath.Abs(assetsPath + "rep_server.key")
	Expect(err).NotTo(HaveOccurred())
	auctioneerServerCert, err := filepath.Abs(assetsPath + "auctioneer_server.crt")
	Expect(err).NotTo(HaveOccurred())
	auctioneerServerKey, err := filepath.Abs(assetsPath + "auctioneer_server.key")
	Expect(err).NotTo(HaveOccurred())
	clientCrt, err := filepath.Abs(assetsPath + "client.crt")
	Expect(err).NotTo(HaveOccurred())
	clientKey, err := filepath.Abs(assetsPath + "client.key")
	Expect(err).NotTo(HaveOccurred())
	caCert, err := filepath.Abs(assetsPath + "ca.crt")
	Expect(err).NotTo(HaveOccurred())

	sqlCACert, err := filepath.Abs(assetsPath + "sql-certs/server-ca.crt")
	Expect(err).NotTo(HaveOccurred())

	sslConfig := world.SSLConfig{
		ServerCert: bbsServerCert,
		ServerKey:  bbsServerKey,
		ClientCert: clientCrt,
		ClientKey:  clientKey,
		CACert:     caCert,
	}

	locketSSLConfig := world.SSLConfig{
		ServerCert: bbsServerCert,
		ServerKey:  bbsServerKey,
		ClientCert: clientCrt,
		ClientKey:  clientKey,
		CACert:     caCert,
	}

	repSSLConfig := world.SSLConfig{
		ServerCert: repServerCert,
		ServerKey:  repServerKey,
		ClientCert: clientCrt,
		ClientKey:  clientKey,
		CACert:     caCert,
	}

	auctioneerSSLConfig := world.SSLConfig{
		ServerCert: auctioneerServerCert,
		ServerKey:  auctioneerServerKey,
		ClientCert: clientCrt,
		ClientKey:  clientKey,
		CACert:     caCert,
	}

	storeTimestamp := time.Now().UnixNano

	unprivilegedGrootfsConfig := world.GrootFSConfig{
		StorePath: fmt.Sprintf("/mnt/btrfs/unprivileged-%d-%d", ginkgo.GinkgoParallelNode(), storeTimestamp),
		FSDriver:  "btrfs",
		DraxBin:   "/usr/local/bin/drax",
		LogLevel:  "debug",
	}
	unprivilegedGrootfsConfig.Create.JSON = true
	unprivilegedGrootfsConfig.Create.UidMappings = []string{"0:4294967294:1", "1:1:4294967293"}
	unprivilegedGrootfsConfig.Create.GidMappings = []string{"0:4294967294:1", "1:1:4294967293"}
	unprivilegedGrootfsConfig.Create.SkipLayerValidation = true

	privilegedGrootfsConfig := world.GrootFSConfig{
		StorePath: fmt.Sprintf("/mnt/btrfs/privileged-%d-%d", ginkgo.GinkgoParallelNode(), storeTimestamp),
		FSDriver:  "btrfs",
		DraxBin:   "/usr/local/bin/drax",
		LogLevel:  "debug",
	}
	privilegedGrootfsConfig.Create.JSON = true
	privilegedGrootfsConfig.Create.SkipLayerValidation = true

	gardenConfig := world.GardenSettingsConfig{
		GrootFSBinPath:            grootfsBinPath,
		GardenBinPath:             gardenBinPath,
		GardenGraphPath:           gardenGraphPath,
		UnprivilegedGrootfsConfig: unprivilegedGrootfsConfig,
		PrivilegedGrootfsConfig:   privilegedGrootfsConfig,
	}

	guid, err := uuid.NewV4()
	Expect(err).NotTo(HaveOccurred())

	volmanConfigDir := world.TempDir(guid.String())

	node := ginkgo.GinkgoParallelNode()
	startPort := 1000 * node
	portRange := 200
	endPort := startPort + portRange*(node+1)

	portAllocator, err := portauthority.New(startPort, endPort)
	Expect(err).NotTo(HaveOccurred())

	dbDriverName, dbBaseConnectionString := DBInfo()

	return world.ComponentMaker{
		Artifacts: builtArtifacts,
		Addresses: worldAddresses,

		RootFSes: stackPathMap,

		ExternalAddress: externalAddress,

		GardenConfig:          gardenConfig,
		SSHConfig:             sshKeys,
		BbsSSL:                sslConfig,
		LocketSSL:             locketSSLConfig,
		RepSSL:                repSSLConfig,
		AuctioneerSSL:         auctioneerSSLConfig,
		SQLCACertFile:         sqlCACert,
		VolmanDriverConfigDir: volmanConfigDir,

		DBDriverName:           dbDriverName,
		DBBaseConnectionString: dbBaseConnectionString,

		PortAllocator: portAllocator,
	}
}
