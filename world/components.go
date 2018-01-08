package world

import (
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"time"

	yaml "gopkg.in/yaml.v2"

	auctioneerconfig "code.cloudfoundry.org/auctioneer/cmd/auctioneer/config"
	"code.cloudfoundry.org/bbs"
	bbsconfig "code.cloudfoundry.org/bbs/cmd/bbs/config"
	bbsrunner "code.cloudfoundry.org/bbs/cmd/bbs/testrunner"
	"code.cloudfoundry.org/bbs/encryption"
	"code.cloudfoundry.org/bbs/serviceclient"
	"code.cloudfoundry.org/bbs/test_helpers"
	"code.cloudfoundry.org/clock"
	"code.cloudfoundry.org/consuladapter"
	"code.cloudfoundry.org/consuladapter/consulrunner"
	loggingclient "code.cloudfoundry.org/diego-logging-client"
	sshproxyconfig "code.cloudfoundry.org/diego-ssh/cmd/ssh-proxy/config"
	"code.cloudfoundry.org/diego-ssh/keys"
	"code.cloudfoundry.org/durationjson"
	executorinit "code.cloudfoundry.org/executor/initializer"
	fileserverconfig "code.cloudfoundry.org/fileserver/cmd/file-server/config"
	"code.cloudfoundry.org/garden"
	gardenclient "code.cloudfoundry.org/garden/client"
	gardenconnection "code.cloudfoundry.org/garden/client/connection"
	"code.cloudfoundry.org/guardian/gqt/runner"
	"code.cloudfoundry.org/inigo/helpers/portauthority"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/lager/lagerflags"
	"code.cloudfoundry.org/locket"
	locketconfig "code.cloudfoundry.org/locket/cmd/locket/config"
	locketrunner "code.cloudfoundry.org/locket/cmd/locket/testrunner"
	repconfig "code.cloudfoundry.org/rep/cmd/rep/config"
	"code.cloudfoundry.org/rep/maintain"
	routeemitterconfig "code.cloudfoundry.org/route-emitter/cmd/route-emitter/config"
	routingapi "code.cloudfoundry.org/route-emitter/cmd/route-emitter/runners"
	"code.cloudfoundry.org/voldriver"
	"code.cloudfoundry.org/voldriver/driverhttp"
	"code.cloudfoundry.org/volman"
	volmanclient "code.cloudfoundry.org/volman/vollocal"
	"github.com/go-sql-driver/mysql"
	_ "github.com/go-sql-driver/mysql"
	"github.com/lib/pq"
	_ "github.com/lib/pq"
	uuid "github.com/nu7hatch/gouuid"
	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/config"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"
	"golang.org/x/crypto/ssh"
)

var (
	PreloadedStacks = []string{"red-stack", "blue-stack", "cflinuxfs2"}
	DefaultStack    = PreloadedStacks[0]
)

type (
	BuiltExecutables map[string]string
	BuiltLifecycles  map[string]string
)

const (
	LifecycleFilename = "lifecycle.tar.gz"
)

type BuiltArtifacts struct {
	Executables BuiltExecutables
	Lifecycles  BuiltLifecycles
	Healthcheck string
}

type SSHKeys struct {
	HostKey       ssh.Signer
	HostKeyPem    string
	PrivateKeyPem string
	AuthorizedKey string
}

type SSLConfig struct {
	ServerCert string
	ServerKey  string
	ClientCert string
	ClientKey  string
	CACert     string
}

type GardenSettingsConfig struct {
	GrootFSBinPath            string
	GardenBinPath             string
	GardenGraphPath           string
	UnprivilegedGrootfsConfig GrootFSConfig
	PrivilegedGrootfsConfig   GrootFSConfig
}

type GrootFSConfig struct {
	StorePath string `yaml:"store"`
	FSDriver  string `yaml:"driver"`
	DraxBin   string `yaml:"drax_bin"`
	LogLevel  string `yaml:"log_level"`
	Create    struct {
		JSON                bool     `yaml:"json"`
		UidMappings         []string `yaml:"uid_mappings"`
		GidMappings         []string `yaml:"gid_mappings"`
		SkipLayerValidation bool     `yaml:"skip_layer_validation"`
	}
}

type ComponentAddresses struct {
	NATS                string
	Consul              string
	BBS                 string
	Health              string
	Rep                 string
	FileServer          string
	Router              string
	GardenLinux         string
	Auctioneer          string
	SSHProxy            string
	SSHProxyHealthCheck string
	FakeVolmanDriver    string
	LocalNodePlugin     string
	Locket              string
	SQL                 string
}

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

func makeCommonComponentMaker(assetsPath string, builtArtifacts BuiltArtifacts, worldAddresses ComponentAddresses) commonComponentMaker {
	grootfsBinPath := os.Getenv("GROOTFS_BINPATH")
	gardenBinPath := os.Getenv("GARDEN_BINPATH")
	gardenRootFSPath := os.Getenv("GARDEN_ROOTFS")
	gardenGraphPath := os.Getenv("GARDEN_GRAPH_PATH")

	if gardenGraphPath == "" {
		gardenGraphPath = os.TempDir()
	}

	if UseGrootFS() {
		Expect(grootfsBinPath).NotTo(BeEmpty(), "must provide $GROOTFS_BINPATH")
	}
	Expect(gardenBinPath).NotTo(BeEmpty(), "must provide $GARDEN_BINPATH")
	Expect(gardenRootFSPath).NotTo(BeEmpty(), "must provide $GARDEN_ROOTFS")

	// tests depend on this env var to be set
	externalAddress := os.Getenv("EXTERNAL_ADDRESS")
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

	sshKeys := SSHKeys{
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

	sslConfig := SSLConfig{
		ServerCert: bbsServerCert,
		ServerKey:  bbsServerKey,
		ClientCert: clientCrt,
		ClientKey:  clientKey,
		CACert:     caCert,
	}

	locketSSLConfig := SSLConfig{
		ServerCert: bbsServerCert,
		ServerKey:  bbsServerKey,
		ClientCert: clientCrt,
		ClientKey:  clientKey,
		CACert:     caCert,
	}

	repSSLConfig := SSLConfig{
		ServerCert: repServerCert,
		ServerKey:  repServerKey,
		ClientCert: clientCrt,
		ClientKey:  clientKey,
		CACert:     caCert,
	}

	auctioneerSSLConfig := SSLConfig{
		ServerCert: auctioneerServerCert,
		ServerKey:  auctioneerServerKey,
		ClientCert: clientCrt,
		ClientKey:  clientKey,
		CACert:     caCert,
	}

	storeTimestamp := time.Now().UnixNano

	unprivilegedGrootfsConfig := GrootFSConfig{
		StorePath: fmt.Sprintf("/mnt/btrfs/unprivileged-%d-%d", GinkgoParallelNode(), storeTimestamp),
		FSDriver:  "btrfs",
		DraxBin:   "/usr/local/bin/drax",
		LogLevel:  "debug",
	}
	unprivilegedGrootfsConfig.Create.JSON = true
	unprivilegedGrootfsConfig.Create.UidMappings = []string{"0:4294967294:1", "1:1:4294967293"}
	unprivilegedGrootfsConfig.Create.GidMappings = []string{"0:4294967294:1", "1:1:4294967293"}
	unprivilegedGrootfsConfig.Create.SkipLayerValidation = true

	privilegedGrootfsConfig := GrootFSConfig{
		StorePath: fmt.Sprintf("/mnt/btrfs/privileged-%d-%d", GinkgoParallelNode(), storeTimestamp),
		FSDriver:  "btrfs",
		DraxBin:   "/usr/local/bin/drax",
		LogLevel:  "debug",
	}
	privilegedGrootfsConfig.Create.JSON = true
	privilegedGrootfsConfig.Create.SkipLayerValidation = true

	gardenConfig := GardenSettingsConfig{
		GrootFSBinPath:            grootfsBinPath,
		GardenBinPath:             gardenBinPath,
		GardenGraphPath:           gardenGraphPath,
		UnprivilegedGrootfsConfig: unprivilegedGrootfsConfig,
		PrivilegedGrootfsConfig:   privilegedGrootfsConfig,
	}

	guid, err := uuid.NewV4()
	Expect(err).NotTo(HaveOccurred())

	volmanConfigDir := TempDir(guid.String())

	node := GinkgoParallelNode()
	startPort := 1000 * node
	portRange := 200
	endPort := startPort + portRange*(node+1)

	portAllocator, err := portauthority.New(startPort, endPort)
	Expect(err).NotTo(HaveOccurred())

	dbDriverName, dbBaseConnectionString := DBInfo()
	return commonComponentMaker{
		artifacts: builtArtifacts,
		addresses: worldAddresses,

		rootFSes: stackPathMap,

		gardenConfig:           gardenConfig,
		sshConfig:              sshKeys,
		bbsSSL:                 sslConfig,
		locketSSL:              locketSSLConfig,
		repSSL:                 repSSLConfig,
		auctioneerSSL:          auctioneerSSLConfig,
		sqlCACertFile:          sqlCACert,
		volmanDriverConfigDir:  volmanConfigDir,
		dbDriverName:           dbDriverName,
		dbBaseConnectionString: dbBaseConnectionString,

		portAllocator: portAllocator,
	}
}

func MakeV0ComponentMaker(assetsPath string, builtArtifacts BuiltArtifacts, worldAddresses ComponentAddresses) ComponentMaker {
	return v0ComponentMaker{commonComponentMaker: makeCommonComponentMaker(assetsPath, builtArtifacts, worldAddresses)}
}

func MakeComponentMaker(assetsPath string, builtArtifacts BuiltArtifacts, worldAddresses ComponentAddresses) ComponentMaker {
	return v1ComponentMaker{commonComponentMaker: makeCommonComponentMaker(assetsPath, builtArtifacts, worldAddresses)}
}

type ComponentMaker interface {
	VolmanDriverConfigDir() string
	SSHConfig() SSHKeys
	Artifacts() BuiltArtifacts
	PortAllocator() portauthority.PortAllocator
	Addresses() ComponentAddresses
	Auctioneer(modifyConfigFuncs ...func(cfg *auctioneerconfig.AuctioneerConfig)) ifrit.Runner
	BBS(modifyConfigFuncs ...func(*bbsconfig.BBSConfig)) ifrit.Runner
	BBSClient() bbs.InternalClient
	BBSServiceClient(logger lager.Logger) serviceclient.ServiceClient
	BBSURL() string
	BBSSSLConfig() SSLConfig
	Consul(argv ...string) ifrit.Runner
	ConsulCluster() string
	CsiLocalNodePlugin(logger lager.Logger) ifrit.Runner
	DefaultStack() string
	FileServer() (ifrit.Runner, string)
	Garden() ifrit.Runner
	GardenClient() garden.Client
	GardenWithoutDefaultStack() ifrit.Runner
	GrootFSDeleteStore()
	GrootFSInitStore()
	Locket(modifyConfigFuncs ...func(*locketconfig.LocketConfig)) ifrit.Runner
	NATS(argv ...string) ifrit.Runner
	Rep(modifyConfigFuncs ...func(*repconfig.RepConfig)) *ginkgomon.Runner
	RepN(n int, modifyConfigFuncs ...func(*repconfig.RepConfig)) *ginkgomon.Runner
	RouteEmitter() ifrit.Runner
	RouteEmitterN(n int, fs ...func(config *routeemitterconfig.RouteEmitterConfig)) ifrit.Runner
	Router() ifrit.Runner
	RoutingAPI(modifyConfigFuncs ...func(*routingapi.Config)) *routingapi.RoutingAPIRunner
	SQL(argv ...string) ifrit.Runner
	SSHProxy(argv ...string) ifrit.Runner
	Setup()
	Teardown()
	VolmanClient(logger lager.Logger) (volman.Manager, ifrit.Runner)
	VolmanDriver(logger lager.Logger) (ifrit.Runner, voldriver.Driver)
}

type commonComponentMaker struct {
	artifacts              BuiltArtifacts
	addresses              ComponentAddresses
	rootFSes               repconfig.RootFSes
	gardenConfig           GardenSettingsConfig
	sshConfig              SSHKeys
	bbsSSL                 SSLConfig
	locketSSL              SSLConfig
	repSSL                 SSLConfig
	auctioneerSSL          SSLConfig
	sqlCACertFile          string
	volmanDriverConfigDir  string
	dbDriverName           string
	dbBaseConnectionString string
	portAllocator          portauthority.PortAllocator
}

func (maker commonComponentMaker) VolmanDriverConfigDir() string {
	return maker.volmanDriverConfigDir
}

func (maker commonComponentMaker) SSHConfig() SSHKeys {
	return maker.sshConfig
}

func (maker commonComponentMaker) PortAllocator() portauthority.PortAllocator {
	return maker.portAllocator
}

func (maker commonComponentMaker) Artifacts() BuiltArtifacts {
	return maker.artifacts
}

func (maker commonComponentMaker) Addresses() ComponentAddresses {
	return maker.addresses
}

func (maker commonComponentMaker) BBSSSLConfig() SSLConfig {
	return maker.bbsSSL
}

func (maker commonComponentMaker) Setup() {
	if UseGrootFS() {
		maker.GrootFSInitStore()
	}
}

func (maker commonComponentMaker) Teardown() {
	if UseGrootFS() {
		maker.GrootFSDeleteStore()
	}
}

func (maker commonComponentMaker) NATS(argv ...string) ifrit.Runner {
	host, port, err := net.SplitHostPort(maker.addresses.NATS)
	Expect(err).NotTo(HaveOccurred())

	return ginkgomon.New(ginkgomon.Config{
		Name:              "gnatsd",
		AnsiColorCode:     "30m",
		StartCheck:        "gnatsd is ready",
		StartCheckTimeout: 10 * time.Second,
		Command: exec.Command(
			"gnatsd",
			append([]string{
				"--addr", host,
				"--port", port,
			}, argv...)...,
		),
	})
}

func (maker commonComponentMaker) SQL(argv ...string) ifrit.Runner {
	return ifrit.RunFunc(func(signals <-chan os.Signal, ready chan<- struct{}) error {
		defer GinkgoRecover()
		dbConnectionString := appendExtraConnectionStringParam(maker.dbDriverName, maker.dbBaseConnectionString, maker.sqlCACertFile)

		db, err := sql.Open(maker.dbDriverName, dbConnectionString)
		Expect(err).NotTo(HaveOccurred())
		defer db.Close()

		Eventually(db.Ping).Should(Succeed())

		_, err = db.Exec(fmt.Sprintf("CREATE DATABASE diego_%d", GinkgoParallelNode()))
		Expect(err).NotTo(HaveOccurred())

		sqlDBName := fmt.Sprintf("diego_%d", GinkgoParallelNode())
		dbWithDatabaseNameConnectionString := appendExtraConnectionStringParam(maker.dbDriverName, fmt.Sprintf("%s%s", maker.dbBaseConnectionString, sqlDBName), maker.sqlCACertFile)
		db, err = sql.Open(maker.dbDriverName, dbWithDatabaseNameConnectionString)
		Expect(err).NotTo(HaveOccurred())
		Eventually(db.Ping).Should(Succeed())

		Expect(db.Close()).To(Succeed())

		close(ready)

		select {
		case <-signals:
			db, err := sql.Open(maker.dbDriverName, dbConnectionString)
			Expect(err).NotTo(HaveOccurred())
			Eventually(db.Ping).ShouldNot(HaveOccurred())

			_, err = db.Exec(fmt.Sprintf("DROP DATABASE %s", sqlDBName))
			Expect(err).NotTo(HaveOccurred())
		}

		return nil
	})
}

func (maker commonComponentMaker) Consul(argv ...string) ifrit.Runner {
	_, port, err := net.SplitHostPort(maker.addresses.Consul)
	Expect(err).NotTo(HaveOccurred())
	httpPort, err := strconv.Atoi(port)
	Expect(err).NotTo(HaveOccurred())

	startingPort := httpPort - consulrunner.PortOffsetHTTP

	clusterRunner := consulrunner.NewClusterRunner(
		consulrunner.ClusterRunnerConfig{
			StartingPort: startingPort,
			NumNodes:     1,
			Scheme:       "http",
		},
	)
	return ifrit.RunFunc(func(signals <-chan os.Signal, ready chan<- struct{}) error {
		defer GinkgoRecover()

		done := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			clusterRunner.Start()
			close(done)
		}()

		Eventually(done, 10).Should(BeClosed())

		close(ready)

		select {
		case <-signals:
			clusterRunner.Stop()
		}

		return nil
	})
}

func (maker commonComponentMaker) GrootFSInitStore() {
	err := maker.grootfsInitStore(maker.gardenConfig.UnprivilegedGrootfsConfig)
	Expect(err).NotTo(HaveOccurred())

	err = maker.grootfsInitStore(maker.gardenConfig.PrivilegedGrootfsConfig)
	Expect(err).NotTo(HaveOccurred())
}

func (maker commonComponentMaker) grootfsInitStore(grootfsConfig GrootFSConfig) error {
	grootfsArgs := []string{}
	grootfsArgs = append(grootfsArgs, "--config", maker.grootfsConfigPath(grootfsConfig))
	grootfsArgs = append(grootfsArgs, "init-store")
	for _, mapping := range grootfsConfig.Create.UidMappings {
		grootfsArgs = append(grootfsArgs, "--uid-mapping", mapping)
	}
	for _, mapping := range grootfsConfig.Create.GidMappings {
		grootfsArgs = append(grootfsArgs, "--gid-mapping", mapping)
	}

	return maker.grootfsRunner(grootfsArgs)
}

func (maker commonComponentMaker) GrootFSDeleteStore() {
	err := maker.grootfsDeleteStore(maker.gardenConfig.UnprivilegedGrootfsConfig)
	Expect(err).NotTo(HaveOccurred())

	err = maker.grootfsDeleteStore(maker.gardenConfig.PrivilegedGrootfsConfig)
	Expect(err).NotTo(HaveOccurred())
}

func (maker commonComponentMaker) grootfsDeleteStore(grootfsConfig GrootFSConfig) error {
	grootfsArgs := []string{}
	grootfsArgs = append(grootfsArgs, "--config", maker.grootfsConfigPath(grootfsConfig))
	grootfsArgs = append(grootfsArgs, "delete-store")
	return maker.grootfsRunner(grootfsArgs)
}

func (maker commonComponentMaker) grootfsRunner(args []string) error {
	cmd := exec.Command(filepath.Join(maker.gardenConfig.GrootFSBinPath, "grootfs"), args...)
	cmd.Stderr = GinkgoWriter
	cmd.Stdout = GinkgoWriter
	return cmd.Run()
}

func (maker commonComponentMaker) grootfsConfigPath(grootfsConfig GrootFSConfig) string {
	configFile, err := ioutil.TempFile("", "grootfs-config")
	Expect(err).NotTo(HaveOccurred())
	defer configFile.Close()
	data, err := yaml.Marshal(&grootfsConfig)
	Expect(err).NotTo(HaveOccurred())
	_, err = configFile.Write(data)
	Expect(err).NotTo(HaveOccurred())

	return configFile.Name()
}

func (maker commonComponentMaker) GardenWithoutDefaultStack() ifrit.Runner {
	return maker.garden(false)
}

func (maker commonComponentMaker) Garden() ifrit.Runner {
	return maker.garden(true)
}

func (maker commonComponentMaker) garden(includeDefaultStack bool) ifrit.Runner {
	defaultRootFS := ""
	if includeDefaultStack {
		defaultRootFS = maker.rootFSes.StackPathMap()[maker.DefaultStack()]
	}

	members := []grouper.Member{}

	config := runner.DefaultGdnRunnerConfig()

	config.GdnBin = maker.artifacts.Executables["garden"]
	config.TarBin = filepath.Join(maker.gardenConfig.GardenBinPath, "tar")
	config.InitBin = filepath.Join(maker.gardenConfig.GardenBinPath, "init")
	config.ExecRunnerBin = filepath.Join(maker.gardenConfig.GardenBinPath, "dadoo")
	config.NSTarBin = filepath.Join(maker.gardenConfig.GardenBinPath, "nstar")
	config.RuntimePluginBin = filepath.Join(maker.gardenConfig.GardenBinPath, "runc")
	ports, err := maker.portAllocator.ClaimPorts(10)
	startPort := int(ports)
	poolSize := 10
	Expect(err).NotTo(HaveOccurred())
	config.PortPoolStart = &startPort
	config.PortPoolSize = &poolSize

	config.DefaultRootFS = defaultRootFS

	config.AllowHostAccess = boolPtr(true)

	config.DenyNetworks = []string{"0.0.0.0/0"}

	host, port, err := net.SplitHostPort(maker.addresses.GardenLinux)
	Expect(err).NotTo(HaveOccurred())

	config.BindSocket = ""
	config.BindIP = host

	intPort, err := strconv.Atoi(port)
	Expect(err).NotTo(HaveOccurred())
	config.BindPort = intPtr(intPort)

	if UseGrootFS() {
		config.ImagePluginBin = filepath.Join(maker.gardenConfig.GrootFSBinPath, "grootfs")
		config.PrivilegedImagePluginBin = filepath.Join(maker.gardenConfig.GrootFSBinPath, "grootfs")

		config.ImagePluginExtraArgs = []string{
			"\"--config\"",
			maker.grootfsConfigPath(maker.gardenConfig.UnprivilegedGrootfsConfig),
		}

		config.PrivilegedImagePluginExtraArgs = []string{
			"\"--config\"",
			maker.grootfsConfigPath(maker.gardenConfig.PrivilegedGrootfsConfig),
		}
	}

	gardenRunner := runner.NewGardenRunner(config)

	members = append(members, grouper.Member{Name: "garden", Runner: gardenRunner})

	return grouper.NewOrdered(os.Interrupt, members)
}

func (maker commonComponentMaker) RoutingAPI(modifyConfigFuncs ...func(*routingapi.Config)) *routingapi.RoutingAPIRunner {
	binPath := maker.artifacts.Executables["routing-api"]

	sqlConfig := routingapi.SQLConfig{
		DriverName: maker.dbDriverName,
		DBName:     fmt.Sprintf("routingapi_%d", GinkgoParallelNode()),
	}

	port, err := maker.portAllocator.ClaimPorts(2)
	Expect(err).NotTo(HaveOccurred())

	if maker.dbDriverName == "mysql" {
		sqlConfig.Port = 3306
		sqlConfig.Username = "diego"
		sqlConfig.Password = "diego_password"
	} else {
		sqlConfig.Port = 5432
		sqlConfig.Username = "diego"
		sqlConfig.Password = "diego_pw"
	}

	runner, err := routingapi.NewRoutingAPIRunner(binPath, maker.ConsulCluster(), int(port), int(port+1), sqlConfig, modifyConfigFuncs...)
	Expect(err).NotTo(HaveOccurred())
	return runner
}

func (maker commonComponentMaker) Locket(modifyConfigFuncs ...func(*locketconfig.LocketConfig)) ifrit.Runner {
	return locketrunner.NewLocketRunner(maker.artifacts.Executables["locket"], func(cfg *locketconfig.LocketConfig) {
		cfg.CertFile = maker.locketSSL.ServerCert
		cfg.KeyFile = maker.locketSSL.ServerKey
		cfg.CaFile = maker.locketSSL.CACert
		cfg.ConsulCluster = maker.ConsulCluster()
		cfg.DatabaseConnectionString = maker.addresses.SQL
		cfg.DatabaseDriver = maker.dbDriverName
		cfg.ListenAddress = maker.addresses.Locket
		cfg.SQLCACertFile = maker.sqlCACertFile
		cfg.LagerConfig = lagerflags.LagerConfig{
			LogLevel: "debug",
		}

		for _, modifyConfig := range modifyConfigFuncs {
			modifyConfig(cfg)
		}
	})
}

func (maker commonComponentMaker) RouteEmitterN(n int, fs ...func(config *routeemitterconfig.RouteEmitterConfig)) ifrit.Runner {
	name := "route-emitter-" + strconv.Itoa(n)

	configFile, err := ioutil.TempFile("", "file-server-config")
	Expect(err).NotTo(HaveOccurred())

	cfg := routeemitterconfig.RouteEmitterConfig{
		ConsulSessionName: name,
		NATSAddresses:     maker.addresses.NATS,
		BBSAddress:        maker.BBSURL(),
		LockRetryInterval: durationjson.Duration(time.Second),
		ConsulCluster:     maker.ConsulCluster(),
		LagerConfig:       lagerflags.LagerConfig{LogLevel: "debug"},
		BBSClientCertFile: maker.bbsSSL.ClientCert,
		BBSClientKeyFile:  maker.bbsSSL.ClientKey,
		BBSCACertFile:     maker.bbsSSL.CACert,
	}

	for _, f := range fs {
		f(&cfg)
	}

	encoder := json.NewEncoder(configFile)
	err = encoder.Encode(&cfg)
	Expect(err).NotTo(HaveOccurred())

	return ginkgomon.New(ginkgomon.Config{
		Name:              name,
		AnsiColorCode:     "36m",
		StartCheck:        `"` + name + `.watcher.sync.complete"`,
		StartCheckTimeout: 10 * time.Second,
		Command: exec.Command(
			maker.artifacts.Executables["route-emitter"],
			"-config", configFile.Name(),
		),
		Cleanup: func() {
			configFile.Close()
			os.RemoveAll(configFile.Name())
		},
	})
}

func (maker commonComponentMaker) FileServer() (ifrit.Runner, string) {
	servedFilesDir := TempDir("file-server-files")

	configFile, err := ioutil.TempFile("", "file-server-config")
	Expect(err).NotTo(HaveOccurred())

	cfg := fileserverconfig.FileServerConfig{
		ServerAddress:   maker.addresses.FileServer,
		ConsulCluster:   maker.ConsulCluster(),
		LagerConfig:     lagerflags.LagerConfig{LogLevel: "debug"},
		StaticDirectory: servedFilesDir,
	}

	buildpackAppLifeCycleDir := filepath.Join(servedFilesDir, "buildpack_app_lifecycle")
	err = os.Mkdir(buildpackAppLifeCycleDir, 0755)
	Expect(err).NotTo(HaveOccurred())
	file := maker.artifacts.Lifecycles["buildpackapplifecycle"]
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		err = exec.Command("cp", file, filepath.Join(buildpackAppLifeCycleDir, "buildpack_app_lifecycle.tgz")).Run()
		Expect(err).NotTo(HaveOccurred())
	}

	dockerAppLifeCycleDir := filepath.Join(servedFilesDir, "docker_app_lifecycle")
	err = os.Mkdir(dockerAppLifeCycleDir, 0755)
	Expect(err).NotTo(HaveOccurred())
	file = maker.artifacts.Lifecycles["dockerapplifecycle"]
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		err = exec.Command("cp", file, filepath.Join(dockerAppLifeCycleDir, "docker_app_lifecycle.tgz")).Run()
		Expect(err).NotTo(HaveOccurred())
	}

	encoder := json.NewEncoder(configFile)
	err = encoder.Encode(&cfg)
	Expect(err).NotTo(HaveOccurred())

	return ginkgomon.New(ginkgomon.Config{
		Name:              "file-server",
		AnsiColorCode:     "92m",
		StartCheck:        `"file-server.ready"`,
		StartCheckTimeout: 10 * time.Second,
		Command: exec.Command(
			maker.artifacts.Executables["file-server"],
			"-config", configFile.Name(),
		),
		Cleanup: func() {
			err := os.RemoveAll(servedFilesDir)
			Expect(err).NotTo(HaveOccurred())
			configFile.Close()
			err = os.RemoveAll(configFile.Name())
			Expect(err).NotTo(HaveOccurred())
		},
	}), servedFilesDir
}

func (maker commonComponentMaker) Router() ifrit.Runner {
	_, routerPort, err := net.SplitHostPort(maker.addresses.Router)
	Expect(err).NotTo(HaveOccurred())

	routerPortInt, err := strconv.Atoi(routerPort)
	Expect(err).NotTo(HaveOccurred())

	natsHost, natsPort, err := net.SplitHostPort(maker.addresses.NATS)
	Expect(err).NotTo(HaveOccurred())

	natsPortInt, err := strconv.Atoi(natsPort)
	Expect(err).NotTo(HaveOccurred())

	routerConfig := `
status:
  port: 0
  user: ""
  pass: ""
nats:
- host: %s
  port: %d
  user: ""
  pass: ""
logging:
  file: /dev/stdout
  syslog: ""
  level: info
  loggregator_enabled: false
  metron_address: 127.0.0.1:65534
port: %d
index: 0
zone: ""
tracing:
  enable_zipkin: false
trace_key: ""
access_log:
  file: ""
  enable_streaming: false
enable_access_log_streaming: false
debug_addr: ""
enable_proxy: false
enable_ssl: false
ssl_port: 0
ssl_cert_path: ""
ssl_key_path: ""
sslcertificate:
  certificate: []
  privatekey: null
  ocspstaple: []
  signedcertificatetimestamps: []
  leaf: null
skip_ssl_validation: false
cipher_suites: "TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256"
ciphersuites: []
load_balancer_healthy_threshold: 0s
publish_start_message_interval: 0s
suspend_pruning_if_nats_unavailable: false
prune_stale_droplets_interval: 5s
droplet_stale_threshold: 10s
publish_active_apps_interval: 0s
start_response_delay_interval: 1s
endpoint_timeout: 0s
route_services_timeout: 0s
secure_cookies: false
oauth:
  token_endpoint: ""
  port: 0
  skip_ssl_validation: false
  client_name: ""
  client_secret: ""
  ca_certs: ""
routing_api:
  uri: ""
  port: 0
  auth_disabled: false
route_services_secret: ""
route_services_secret_decrypt_only: ""
route_services_recommend_https: false
extra_headers_to_log: []
token_fetcher_max_retries: 0
token_fetcher_retry_interval: 0s
token_fetcher_expiration_buffer_time: 0
pid_file: ""
`
	routerConfig = fmt.Sprintf(routerConfig, natsHost, uint16(natsPortInt), uint16(routerPortInt))

	configFile, err := ioutil.TempFile(os.TempDir(), "router-config")
	Expect(err).NotTo(HaveOccurred())
	defer configFile.Close()
	_, err = configFile.Write([]byte(routerConfig))
	Expect(err).NotTo(HaveOccurred())

	return ginkgomon.New(ginkgomon.Config{
		Name:              "router",
		AnsiColorCode:     "93m",
		StartCheck:        "router.started",
		StartCheckTimeout: 10 * time.Second, // it waits 1 second before listening. yep.
		Command: exec.Command(
			maker.artifacts.Executables["router"],
			"-c", configFile.Name(),
		),
		Cleanup: func() {
			err := os.Remove(configFile.Name())
			Expect(err).NotTo(HaveOccurred())
		},
	})
}

func (maker commonComponentMaker) SSHProxy(argv ...string) ifrit.Runner {
	sshProxyConfig := sshproxyconfig.SSHProxyConfig{
		Address:            maker.addresses.SSHProxy,
		HealthCheckAddress: maker.addresses.SSHProxyHealthCheck,
		BBSAddress:         maker.BBSURL(),
		BBSCACert:          maker.bbsSSL.CACert,
		BBSClientCert:      maker.bbsSSL.ClientCert,
		BBSClientKey:       maker.bbsSSL.ClientKey,
		ConsulCluster:      maker.ConsulCluster(),
		EnableDiegoAuth:    true,
		HostKey:            maker.sshConfig.HostKeyPem,
		LagerConfig: lagerflags.LagerConfig{
			LogLevel: "debug",
		},
	}

	configFile, err := ioutil.TempFile("", "ssh-proxy-config")
	Expect(err).NotTo(HaveOccurred())
	defer configFile.Close()

	encoder := json.NewEncoder(configFile)
	err = encoder.Encode(&sshProxyConfig)
	Expect(err).NotTo(HaveOccurred())

	return ginkgomon.New(ginkgomon.Config{
		Name:              "ssh-proxy",
		AnsiColorCode:     "96m",
		StartCheck:        "ssh-proxy.started",
		StartCheckTimeout: 10 * time.Second,
		Command: exec.Command(
			maker.artifacts.Executables["ssh-proxy"],
			append([]string{
				"-config", configFile.Name(),
			}, argv...)...,
		),
	})
}

func (maker commonComponentMaker) DefaultStack() string {
	Expect(maker.rootFSes).NotTo(BeEmpty())
	return maker.rootFSes.Names()[0]
}

func (maker commonComponentMaker) GardenClient() garden.Client {
	return gardenclient.New(gardenconnection.New("tcp", maker.addresses.GardenLinux))
}

func (maker commonComponentMaker) BBSClient() bbs.InternalClient {
	client, err := bbs.NewSecureClient(
		maker.BBSURL(),
		maker.bbsSSL.CACert,
		maker.bbsSSL.ClientCert,
		maker.bbsSSL.ClientKey,
		0, 0,
	)
	Expect(err).NotTo(HaveOccurred())
	return client
}

func (maker commonComponentMaker) BBSServiceClient(logger lager.Logger) serviceclient.ServiceClient {
	client, err := consuladapter.NewClientFromUrl(maker.ConsulCluster())
	Expect(err).NotTo(HaveOccurred())

	cellPresenceClient := maintain.NewCellPresenceClient(client, clock.NewClock())
	locketClient := serviceclient.NewNoopLocketClient()

	return serviceclient.NewServiceClient(cellPresenceClient, locketClient)
}

func (maker commonComponentMaker) BBSURL() string {
	return "https://" + maker.addresses.BBS
}

func (maker commonComponentMaker) ConsulCluster() string {
	return "http://" + maker.addresses.Consul
}

func (maker commonComponentMaker) VolmanClient(logger lager.Logger) (volman.Manager, ifrit.Runner) {
	driverConfig := volmanclient.NewDriverConfig()
	driverConfig.DriverPaths = []string{path.Join(maker.volmanDriverConfigDir, fmt.Sprintf("node-%d", config.GinkgoConfig.ParallelNode))}
	driverConfig.CsiPaths = []string{path.Join(maker.volmanDriverConfigDir, fmt.Sprintf("local-node-plugin-%d", config.GinkgoConfig.ParallelNode))}
	driverConfig.CsiMountRootDir = path.Join(maker.volmanDriverConfigDir, "local-node-plugin-mount")

	metronClient, err := loggingclient.NewIngressClient(loggingclient.Config{})
	Expect(err).NotTo(HaveOccurred())
	return volmanclient.NewServer(logger, metronClient, driverConfig)
}

func (maker commonComponentMaker) VolmanDriver(logger lager.Logger) (ifrit.Runner, voldriver.Driver) {
	debugServerPort, err := maker.portAllocator.ClaimPorts(1)
	Expect(err).NotTo(HaveOccurred())
	debugServerAddress := fmt.Sprintf("0.0.0.0:%d", debugServerPort)
	fakeDriverRunner := ginkgomon.New(ginkgomon.Config{
		Name: "local-driver",
		Command: exec.Command(
			maker.artifacts.Executables["local-driver"],
			"-listenAddr", maker.addresses.FakeVolmanDriver,
			"-debugAddr", debugServerAddress,
			"-mountDir", maker.volmanDriverConfigDir,
			"-driversPath", path.Join(maker.volmanDriverConfigDir, fmt.Sprintf("node-%d", config.GinkgoConfig.ParallelNode)),
		),
		StartCheck: "local-driver-server.started",
	})

	client, err := driverhttp.NewRemoteClient("http://"+maker.addresses.FakeVolmanDriver, nil)
	Expect(err).NotTo(HaveOccurred())

	return fakeDriverRunner, client
}

func (maker commonComponentMaker) CsiLocalNodePlugin(logger lager.Logger) ifrit.Runner {
	localNodePluginRunner := ginkgomon.New(ginkgomon.Config{
		Name: "local-node-plugin",
		Command: exec.Command(
			maker.artifacts.Executables["local-node-plugin"],
			"-listenAddr", maker.addresses.LocalNodePlugin,
			"-pluginsPath", path.Join(maker.volmanDriverConfigDir, fmt.Sprintf("local-node-plugin-%d", config.GinkgoConfig.ParallelNode)),
			"-volumesRoot", path.Join(maker.volmanDriverConfigDir, fmt.Sprintf("local-node-volumes-%d", config.GinkgoConfig.ParallelNode)),
		),
		StartCheck: "local-node-plugin.started",
	})

	return localNodePluginRunner
}

func (maker commonComponentMaker) locketClientConfig() locket.ClientLocketConfig {
	return locket.ClientLocketConfig{
		LocketAddress:        maker.addresses.Locket,
		LocketCACertFile:     maker.locketSSL.CACert,
		LocketClientCertFile: maker.locketSSL.ClientCert,
		LocketClientKeyFile:  maker.locketSSL.ClientKey,
	}
}

type v1ComponentMaker struct {
	commonComponentMaker
}

type v0ComponentMaker struct {
	commonComponentMaker
}

func (maker v0ComponentMaker) Auctioneer(_ ...func(*auctioneerconfig.AuctioneerConfig)) ifrit.Runner {
	// TODO: pass a real config to the functions and use it to set the flags

	args := []string{
		"-bbsAddress", maker.BBSURL(),
		"-bbsCACert", maker.bbsSSL.CACert,
		"-bbsClientCert", maker.bbsSSL.ClientCert,
		"-bbsClientKey", maker.bbsSSL.ClientKey,
		"-consulCluster", maker.ConsulCluster(),
		"-listenAddr", maker.addresses.Auctioneer,
		"-lockRetryInterval", "1s",
		"-logLevel", "debug",
		"-repCACert", maker.repSSL.CACert,
		"-repClientCert", maker.repSSL.ClientCert,
		"-repClientKey", maker.repSSL.ClientKey,
		"-startingContainerWeight", "0.33",
	}

	return ginkgomon.New(ginkgomon.Config{
		Name:              "auctioneer",
		AnsiColorCode:     "35m",
		StartCheck:        `"auctioneer.started"`,
		StartCheckTimeout: 10 * time.Second,
		Command: exec.Command(
			maker.artifacts.Executables["auctioneer"],
			args...,
		),
	})
}

func (maker v0ComponentMaker) RouteEmitter() ifrit.Runner {
	return ginkgomon.New(ginkgomon.Config{
		Name:              "route-emitter",
		AnsiColorCode:     "36m",
		StartCheck:        `"route-emitter.started"`,
		StartCheckTimeout: 10 * time.Second,
		Command: exec.Command(
			maker.artifacts.Executables["route-emitter"],
			[]string{
				"-natsAddresses", maker.addresses.NATS,
				"-bbsAddress", maker.BBSURL(),
				"-lockRetryInterval", "1s",
				"-consulCluster", maker.ConsulCluster(),
				"-logLevel", "debug",
				"-bbsClientCert", maker.bbsSSL.ClientCert,
				"-bbsClientKey", maker.bbsSSL.ClientKey,
				"-bbsCACert", maker.bbsSSL.CACert,
			}...,
		),
	})
}

func (maker v0ComponentMaker) FileServer() (ifrit.Runner, string) {
	servedFilesDir := TempDir("file-server-files")

	return ginkgomon.New(ginkgomon.Config{
		Name:              "file-server",
		AnsiColorCode:     "92m",
		StartCheck:        `"file-server.ready"`,
		StartCheckTimeout: 10 * time.Second,
		Command: exec.Command(
			maker.artifacts.Executables["file-server"],
			[]string{
				"-address", maker.addresses.FileServer,
				"-consulCluster", maker.ConsulCluster(),
				"-logLevel", "debug",
				"-staticDirectory", servedFilesDir,
			}...,
		),
		Cleanup: func() {
			err := os.RemoveAll(servedFilesDir)
			Expect(err).NotTo(HaveOccurred())
		},
	}), servedFilesDir
}

func (maker v0ComponentMaker) BBS(modifyConfigFuncs ...func(*bbsconfig.BBSConfig)) ifrit.Runner {
	// TODO: pass a real config to the functions and use it to set the flags

	args := []string{
		"-activeKeyLabel", "secure-key-1",
		"-advertiseURL", maker.BBSURL(),
		"-auctioneerAddress", "http://" + maker.addresses.Auctioneer,
		"-caFile", maker.bbsSSL.CACert,
		"-certFile", maker.bbsSSL.ServerCert,
		"-consulCluster", maker.ConsulCluster(),
		"-databaseConnectionString", maker.addresses.SQL,
		"-databaseDriver", maker.dbDriverName,
		"-encryptionKey", "secure-key-1:secure-passphrase",
		"-etcdCaFile", "",
		"-etcdCertFile", "",
		"-etcdCluster", "",
		"-etcdKeyFile", "",
		"-healthAddress", maker.addresses.Health,
		"-keyFile", maker.bbsSSL.ServerKey,
		"-listenAddress", maker.addresses.BBS,
		"-logLevel", "debug",
		"-repCACert", maker.repSSL.CACert,
		"-repClientCert", maker.repSSL.ClientCert,
		"-repClientKey", maker.repSSL.ClientKey,
		"-requireSSL",
	}

	return ginkgomon.New(ginkgomon.Config{
		Name:              "bbs",
		AnsiColorCode:     "32m",
		StartCheck:        "bbs.started",
		StartCheckTimeout: 10 * time.Second,
		Command: exec.Command(
			maker.artifacts.Executables["bbs"],
			args...,
		),
	})
}

func (maker v0ComponentMaker) Rep(modifyConfigFuncs ...func(*repconfig.RepConfig)) *ginkgomon.Runner {
	return maker.RepN(0, modifyConfigFuncs...)
}

func (maker v0ComponentMaker) RepN(n int, _ ...func(*repconfig.RepConfig)) *ginkgomon.Runner {
	// TODO: pass a real config to the functions and use it to set the flags

	host, portString, err := net.SplitHostPort(maker.addresses.Rep)
	Expect(err).NotTo(HaveOccurred())
	port, err := strconv.Atoi(portString)
	Expect(err).NotTo(HaveOccurred())

	name := "rep-" + strconv.Itoa(n)

	tmpDir := TempDir("executor")
	cachePath := path.Join(tmpDir, "cache")

	args := []string{
		"-sessionName", name,
		"-bbsAddress", maker.BBSURL(),
		"-bbsCACert", maker.bbsSSL.CACert,
		"-bbsClientCert", maker.bbsSSL.ClientCert,
		"-bbsClientKey", maker.bbsSSL.ClientKey,
		"-caFile", maker.repSSL.CACert,
		"-cachePath", cachePath,
		"-cellID", "the-cell-id-" + strconv.Itoa(GinkgoParallelNode()) + "-" + strconv.Itoa(n),
		"-certFile", maker.repSSL.ServerCert,
		"-consulCluster", maker.ConsulCluster(),
		"-containerMaxCpuShares", "1024",
		"-enableLegacyApiServer=false",
		"-evacuationPollingInterval", "1s",
		"-evacuationTimeout", "10s",
		"-gardenAddr", maker.addresses.GardenLinux,
		"-gardenHealthcheckProcessPath", "/bin/sh",
		"-gardenHealthcheckProcessArgs", "-c,echo,foo",
		"-gardenHealthcheckProcessUser", "vcap",
		"-gardenNetwork", "tcp",
		"-keyFile", maker.repSSL.ServerKey,
		"-listenAddr", fmt.Sprintf("%s:%d", host, offsetPort(port, n)),
		"-listenAddrSecurable", fmt.Sprintf("%s:%d", host, offsetPort(port+100, n)),
		"-lockRetryInterval", "1s",
		"-lockTTL", "10s",
		"-logLevel", "debug",
		"-pollingInterval", "1s",
		"-requireTLS=true",
		"-rootFSProvider", "docker",
		"-tempDir", tmpDir,
		"-volmanDriverPaths", path.Join(maker.volmanDriverConfigDir, fmt.Sprintf("node-%d", config.GinkgoConfig.ParallelNode)),
	}

	for _, rootfs := range maker.rootFSes {
		args = append(args, "-preloadedRootFS", fmt.Sprintf("%s:%s", rootfs.Name, rootfs.Path))
	}

	return ginkgomon.New(ginkgomon.Config{
		Name:          name,
		AnsiColorCode: "33m",
		StartCheck:    `"` + name + `.started"`,
		// rep is not started until it can ping an executor and run a healthcheck
		// container on garden; this can take a bit to start, so account for it
		StartCheckTimeout: 2 * time.Minute,
		Command: exec.Command(
			maker.artifacts.Executables["rep"],
			args...,
		),
		Cleanup: func() {
			os.RemoveAll(tmpDir)
		},
	})
}

func (maker v1ComponentMaker) BBS(modifyConfigFuncs ...func(*bbsconfig.BBSConfig)) ifrit.Runner {
	config := bbsconfig.BBSConfig{
		AdvertiseURL:  maker.BBSURL(),
		ConsulCluster: maker.ConsulCluster(),
		EncryptionConfig: encryption.EncryptionConfig{
			ActiveKeyLabel: "secure-key-1",
			EncryptionKeys: map[string]string{
				"secure-key-1": "secure-passphrase",
			},
		},
		LagerConfig: lagerflags.LagerConfig{
			LogLevel: "debug",
		},
		AuctioneerAddress:             "https://" + maker.addresses.Auctioneer,
		ListenAddress:                 maker.addresses.BBS,
		HealthAddress:                 maker.addresses.Health,
		RequireSSL:                    true,
		CertFile:                      maker.bbsSSL.ServerCert,
		KeyFile:                       maker.bbsSSL.ServerKey,
		CaFile:                        maker.bbsSSL.CACert,
		RepCACert:                     maker.repSSL.CACert,
		RepClientCert:                 maker.repSSL.ClientCert,
		RepClientKey:                  maker.repSSL.ClientKey,
		AuctioneerCACert:              maker.auctioneerSSL.CACert,
		AuctioneerClientCert:          maker.auctioneerSSL.ClientCert,
		AuctioneerClientKey:           maker.auctioneerSSL.ClientKey,
		DatabaseConnectionString:      maker.addresses.SQL,
		DatabaseDriver:                maker.dbDriverName,
		DetectConsulCellRegistrations: true,
		AuctioneerRequireTLS:          true,
		SQLCACertFile:                 maker.sqlCACertFile,
		ClientLocketConfig:            maker.locketClientConfig(),
		UUID:                          "bbs-inigo-lock-owner",
	}

	for _, modifyConfig := range modifyConfigFuncs {
		modifyConfig(&config)
	}

	runner := bbsrunner.New(maker.artifacts.Executables["bbs"], config)
	runner.AnsiColorCode = "32m"
	runner.StartCheckTimeout = 10 * time.Second
	return runner
}

func (maker v1ComponentMaker) Rep(modifyConfigFuncs ...func(*repconfig.RepConfig)) *ginkgomon.Runner {
	return maker.RepN(0, modifyConfigFuncs...)
}

func (maker v1ComponentMaker) RepN(n int, modifyConfigFuncs ...func(*repconfig.RepConfig)) *ginkgomon.Runner {
	host, portString, err := net.SplitHostPort(maker.addresses.Rep)
	Expect(err).NotTo(HaveOccurred())
	port, err := strconv.Atoi(portString)
	Expect(err).NotTo(HaveOccurred())

	name := "rep-" + strconv.Itoa(n)

	tmpDir := TempDir("executor")
	cachePath := path.Join(tmpDir, "cache")

	repConfig := repconfig.RepConfig{
		SessionName:               name,
		SupportedProviders:        []string{"docker"},
		BBSAddress:                maker.BBSURL(),
		ListenAddr:                fmt.Sprintf("%s:%d", host, offsetPort(port, n)),
		CellID:                    "the-cell-id-" + strconv.Itoa(GinkgoParallelNode()) + "-" + strconv.Itoa(n),
		PollingInterval:           durationjson.Duration(1 * time.Second),
		EvacuationPollingInterval: durationjson.Duration(1 * time.Second),
		EvacuationTimeout:         durationjson.Duration(1 * time.Second),
		LockTTL:                   durationjson.Duration(10 * time.Second),
		LockRetryInterval:         durationjson.Duration(1 * time.Second),
		ConsulCluster:             maker.ConsulCluster(),
		BBSClientCertFile:         maker.bbsSSL.ClientCert,
		BBSClientKeyFile:          maker.bbsSSL.ClientKey,
		BBSCACertFile:             maker.bbsSSL.CACert,
		ServerCertFile:            maker.repSSL.ServerCert,
		ServerKeyFile:             maker.repSSL.ServerKey,
		CaCertFile:                maker.repSSL.CACert,
		RequireTLS:                true,
		EnableLegacyAPIServer:     false,
		ListenAddrSecurable:       fmt.Sprintf("%s:%d", host, offsetPort(port+100, n)),
		PreloadedRootFS:           maker.rootFSes,
		ExecutorConfig: executorinit.ExecutorConfig{
			GardenNetwork:         "tcp",
			GardenAddr:            maker.addresses.GardenLinux,
			ContainerMaxCpuShares: 1024,
			CachePath:             cachePath,
			TempDir:               tmpDir,
			GardenHealthcheckProcessPath:  "/bin/sh",
			GardenHealthcheckProcessArgs:  []string{"-c", "echo", "foo"},
			GardenHealthcheckProcessUser:  "vcap",
			VolmanDriverPaths:             path.Join(maker.volmanDriverConfigDir, fmt.Sprintf("node-%d", config.GinkgoConfig.ParallelNode)),
			ContainerOwnerName:            "executor-" + strconv.Itoa(n),
			HealthCheckContainerOwnerName: "executor-health-check-" + strconv.Itoa(n),
		},
		LagerConfig: lagerflags.LagerConfig{
			LogLevel: "debug",
		},
	}

	for _, modifyConfig := range modifyConfigFuncs {
		modifyConfig(&repConfig)
	}

	configFile, err := ioutil.TempFile(os.TempDir(), "rep-config")
	Expect(err).NotTo(HaveOccurred())

	defer configFile.Close()

	err = json.NewEncoder(configFile).Encode(repConfig)
	Expect(err).NotTo(HaveOccurred())

	return ginkgomon.New(ginkgomon.Config{
		Name:          name,
		AnsiColorCode: "33m",
		StartCheck:    `"` + name + `.started"`,
		// rep is not started until it can ping an executor and run a healthcheck
		// container on garden; this can take a bit to start, so account for it
		StartCheckTimeout: 2 * time.Minute,
		Command: exec.Command(
			maker.artifacts.Executables["rep"],
			"-config", configFile.Name()),
		Cleanup: func() {
			os.RemoveAll(tmpDir)
		},
	})
}

func (maker v1ComponentMaker) Auctioneer(modifyConfigFuncs ...func(cfg *auctioneerconfig.AuctioneerConfig)) ifrit.Runner {
	auctioneerConfig := auctioneerconfig.AuctioneerConfig{
		BBSAddress:              maker.BBSURL(),
		ListenAddress:           maker.addresses.Auctioneer,
		LockRetryInterval:       durationjson.Duration(time.Second),
		ConsulCluster:           maker.ConsulCluster(),
		BBSClientCertFile:       maker.bbsSSL.ClientCert,
		BBSClientKeyFile:        maker.bbsSSL.ClientKey,
		BBSCACertFile:           maker.bbsSSL.CACert,
		StartingContainerWeight: 0.33,
		RepCACert:               maker.repSSL.CACert,
		RepClientCert:           maker.repSSL.ClientCert,
		RepClientKey:            maker.repSSL.ClientKey,
		CACertFile:              maker.auctioneerSSL.CACert,
		ServerCertFile:          maker.auctioneerSSL.ServerCert,
		ServerKeyFile:           maker.auctioneerSSL.ServerKey,
		LagerConfig: lagerflags.LagerConfig{
			LogLevel: "debug",
		},
		ClientLocketConfig: maker.locketClientConfig(),
		UUID:               "auctioneer-inigo-lock-owner",
	}

	for _, modifyConfig := range modifyConfigFuncs {
		modifyConfig(&auctioneerConfig)
	}

	configFile, err := ioutil.TempFile(os.TempDir(), "auctioneer-config")
	Expect(err).NotTo(HaveOccurred())

	err = json.NewEncoder(configFile).Encode(auctioneerConfig)
	Expect(err).NotTo(HaveOccurred())

	return ginkgomon.New(ginkgomon.Config{
		Name:              "auctioneer",
		AnsiColorCode:     "35m",
		StartCheck:        `"auctioneer.started"`,
		StartCheckTimeout: 10 * time.Second,
		Command: exec.Command(
			maker.artifacts.Executables["auctioneer"],
			"-config", configFile.Name(),
		),
	})
}

func (maker v1ComponentMaker) RouteEmitter() ifrit.Runner {
	return maker.RouteEmitterN(0, func(*routeemitterconfig.RouteEmitterConfig) {})
}

func (blc *BuiltLifecycles) BuildLifecycles(lifeCycle string) {
	lifeCyclePath := filepath.Join("code.cloudfoundry.org", lifeCycle)

	builderPath, err := gexec.BuildIn(os.Getenv("APP_LIFECYCLE_GOPATH"), filepath.Join(lifeCyclePath, "builder"), "-race")
	Expect(err).NotTo(HaveOccurred())

	launcherPath, err := gexec.BuildIn(os.Getenv("APP_LIFECYCLE_GOPATH"), filepath.Join(lifeCyclePath, "launcher"), "-race")
	Expect(err).NotTo(HaveOccurred())

	healthcheckPath, err := gexec.Build("code.cloudfoundry.org/healthcheck/cmd/healthcheck", "-race")
	Expect(err).NotTo(HaveOccurred())

	os.Setenv("CGO_ENABLED", "0")
	diegoSSHPath, err := gexec.Build("code.cloudfoundry.org/diego-ssh/cmd/sshd", "-a", "-installsuffix", "static")
	os.Unsetenv("CGO_ENABLED")
	Expect(err).NotTo(HaveOccurred())

	lifecycleDir := TempDir(lifeCycle)

	err = os.Rename(builderPath, filepath.Join(lifecycleDir, "builder"))
	Expect(err).NotTo(HaveOccurred())

	err = os.Rename(healthcheckPath, filepath.Join(lifecycleDir, "healthcheck"))
	Expect(err).NotTo(HaveOccurred())

	err = os.Rename(launcherPath, filepath.Join(lifecycleDir, "launcher"))
	Expect(err).NotTo(HaveOccurred())

	err = os.Rename(diegoSSHPath, filepath.Join(lifecycleDir, "diego-sshd"))
	Expect(err).NotTo(HaveOccurred())

	cmd := exec.Command("tar", "-czf", "lifecycle.tar.gz", "builder", "launcher", "healthcheck", "diego-sshd")
	cmd.Stderr = GinkgoWriter
	cmd.Stdout = GinkgoWriter
	cmd.Dir = lifecycleDir
	err = cmd.Run()
	Expect(err).NotTo(HaveOccurred())

	(*blc)[lifeCycle] = filepath.Join(lifecycleDir, LifecycleFilename)
}

// offsetPort retuns a new port offest by a given number in such a way
// that it does not interfere with the ginkgo parallel node offest in the base port.
func offsetPort(basePort, offset int) int {
	return basePort + (10 * offset)
}

func appendExtraConnectionStringParam(driverName, databaseConnectionString, sqlCACertFile string) string {
	switch driverName {
	case "mysql":
		cfg, err := mysql.ParseDSN(databaseConnectionString)
		Expect(err).NotTo(HaveOccurred())

		if sqlCACertFile != "" {
			certBytes, err := ioutil.ReadFile(sqlCACertFile)
			Expect(err).NotTo(HaveOccurred())

			caCertPool := x509.NewCertPool()
			Expect(caCertPool.AppendCertsFromPEM(certBytes)).To(BeTrue())

			tlsConfig := &tls.Config{
				InsecureSkipVerify: false,
				RootCAs:            caCertPool,
			}

			mysql.RegisterTLSConfig("bbs-tls", tlsConfig)
			cfg.TLSConfig = "bbs-tls"
		}
		cfg.Timeout = 10 * time.Minute
		cfg.ReadTimeout = 10 * time.Minute
		cfg.WriteTimeout = 10 * time.Minute
		databaseConnectionString = cfg.FormatDSN()
	case "postgres":
		var err error
		databaseConnectionString, err = pq.ParseURL(databaseConnectionString)
		Expect(err).NotTo(HaveOccurred())
		if sqlCACertFile == "" {
			databaseConnectionString = databaseConnectionString + " sslmode=disable"
		} else {
			databaseConnectionString = fmt.Sprintf("%s sslmode=verify-ca sslrootcert=%s", databaseConnectionString, sqlCACertFile)
		}
	}

	return databaseConnectionString
}

func UseGrootFS() bool {
	return os.Getenv("USE_GROOTFS") == "true"
}

func intPtr(i int) *int {
	return &i
}

func boolPtr(b bool) *bool {
	return &b
}
