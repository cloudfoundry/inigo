package volman_test

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"testing"

	"fmt"
	"path"
	"path/filepath"

	"code.cloudfoundry.org/dockerdriver"
	"code.cloudfoundry.org/garden"
	"code.cloudfoundry.org/inigo/helpers"
	"code.cloudfoundry.org/inigo/helpers/certauthority"
	"code.cloudfoundry.org/inigo/helpers/portauthority"
	"code.cloudfoundry.org/inigo/world"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/lager/ginkgoreporter"
	"code.cloudfoundry.org/lager/lagertest"
	"code.cloudfoundry.org/localip"
	"code.cloudfoundry.org/volman"
	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/config"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
)

var (
	componentMaker world.ComponentMaker

	gardenProcess ifrit.Process
	gardenClient  garden.Client

	volmanClient        volman.Manager
	driverSyncer        ifrit.Runner
	driverSyncerProcess ifrit.Process
	localDriverRunner   ifrit.Runner
	localDriverProcess  ifrit.Process

	driverClient    dockerdriver.Driver
	localNodeClient volman.Plugin

	logger lager.Logger

	driverPluginsPath string
	certDepot         string
)

var _ = SynchronizedBeforeSuite(func() []byte {
	payload, err := json.Marshal(world.BuiltArtifacts{
		Executables: CompileTestedExecutables(),
	})
	Expect(err).NotTo(HaveOccurred())

	return payload
}, func(encodedBuiltArtifacts []byte) {
	var builtArtifacts world.BuiltArtifacts

	err := json.Unmarshal(encodedBuiltArtifacts, &builtArtifacts)
	Expect(err).NotTo(HaveOccurred())

	_, dbBaseConnectionString := world.DBInfo()

	localIP, err := localip.LocalIP()
	Expect(err).NotTo(HaveOccurred())

	addresses := world.ComponentAddresses{
		Garden:              fmt.Sprintf("127.0.0.1:%d", 10000+config.GinkgoConfig.ParallelNode),
		NATS:                fmt.Sprintf("127.0.0.1:%d", 11000+config.GinkgoConfig.ParallelNode),
		Rep:                 fmt.Sprintf("127.0.0.1:%d", 14000+config.GinkgoConfig.ParallelNode),
		FileServer:          fmt.Sprintf("%s:%d", localIP, 17000+config.GinkgoConfig.ParallelNode),
		Router:              fmt.Sprintf("127.0.0.1:%d", 18000+config.GinkgoConfig.ParallelNode),
		RouterStatus:        fmt.Sprintf("127.0.0.1:%d", 18100+config.GinkgoConfig.ParallelNode),
		BBS:                 fmt.Sprintf("127.0.0.1:%d", 20500+config.GinkgoConfig.ParallelNode*2),
		Health:              fmt.Sprintf("127.0.0.1:%d", 20500+config.GinkgoConfig.ParallelNode*2+1),
		Auctioneer:          fmt.Sprintf("127.0.0.1:%d", 23000+config.GinkgoConfig.ParallelNode),
		SSHProxy:            fmt.Sprintf("127.0.0.1:%d", 23500+config.GinkgoConfig.ParallelNode),
		SSHProxyHealthCheck: fmt.Sprintf("127.0.0.1:%d", 24500+config.GinkgoConfig.ParallelNode),
		FakeVolmanDriver:    fmt.Sprintf("127.0.0.1:%d", 25500+config.GinkgoConfig.ParallelNode),
		Locket:              fmt.Sprintf("127.0.0.1:%d", 26500+config.GinkgoConfig.ParallelNode),
		SQL:                 fmt.Sprintf("%sdiego_%d", dbBaseConnectionString, config.GinkgoConfig.ParallelNode),
	}

	node := GinkgoParallelProcess()
	startPort := 1000 * node
	portRange := 950
	endPort := startPort + portRange

	allocator, err := portauthority.New(startPort, endPort)
	Expect(err).NotTo(HaveOccurred())

	certDepot, err = ioutil.TempDir("", "cert-depot")
	Expect(err).NotTo(HaveOccurred())

	certAuthority, err := certauthority.NewCertAuthority(certDepot, "ca")
	Expect(err).NotTo(HaveOccurred())

	componentMaker = world.MakeComponentMaker(builtArtifacts, addresses, allocator, certAuthority)
	componentMaker.Setup()
})

var _ = AfterSuite(func() {
	Expect(os.RemoveAll(certDepot)).To(Succeed())
	componentMaker.Teardown()
})

var _ = BeforeEach(func() {
	logger = lagertest.NewTestLogger("volman-inigo-suite")

	gardenProcess = ginkgomon.Invoke(componentMaker.Garden())
	gardenClient = componentMaker.GardenClient()

	localDriverRunner, driverClient = componentMaker.VolmanDriver(logger)
	localDriverProcess = ginkgomon.Invoke(localDriverRunner)

	// make a dummy spec file not corresponding to a running driver just to make sure volman ignores it
	driverPluginsPath = path.Join(componentMaker.VolmanDriverConfigDir(), fmt.Sprintf("node-%d", config.GinkgoConfig.ParallelNode))
	dockerdriver.WriteDriverSpec(logger, driverPluginsPath, "deaddriver", "json", []byte(`{"Name":"deaddriver","Addr":"https://127.0.0.1:1111"}`))

	volmanClient, driverSyncer = componentMaker.VolmanClient(logger)
	driverSyncerProcess = ginkgomon.Invoke(driverSyncer)
})

var _ = AfterEach(func() {
	destroyContainerErrors := helpers.CleanupGarden(gardenClient)

	helpers.StopProcesses(gardenProcess, driverSyncerProcess, localDriverProcess)

	Expect(destroyContainerErrors).To(
		BeEmpty(),
		"%d containers failed to be destroyed!",
		len(destroyContainerErrors),
	)

	os.Remove(filepath.Join(driverPluginsPath, "deaddriver.json"))
})

func TestVolman(t *testing.T) {
	helpers.RegisterDefaultTimeouts()

	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t, "Volman Integration Suite", []Reporter{
		ginkgoreporter.New(GinkgoWriter),
	})
}

func CompileTestedExecutables() world.BuiltExecutables {
	var err error

	cwd, err := os.Getwd()
	Expect(err).NotTo(HaveOccurred())

	builtExecutables := world.BuiltExecutables{}

	Expect(err).NotTo(HaveOccurred())
	Expect(os.Chdir(os.Getenv("GARDEN_GOPATH"))).To(Succeed())
	builtExecutables["garden"], err = gexec.Build("./cmd/gdn", "-race", "-a", "-tags", "daemon")
	Expect(err).NotTo(HaveOccurred())
	Expect(os.Chdir(cwd)).To(Succeed())

	builtExecutables["local-driver"], err = gexec.Build("code.cloudfoundry.org/localdriver/cmd/localdriver", "-race")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["auctioneer"], err = gexec.Build("code.cloudfoundry.org/auctioneer/cmd/auctioneer", "-race")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["rep"], err = gexec.Build("code.cloudfoundry.org/rep/cmd/rep", "-race")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["bbs"], err = gexec.Build("code.cloudfoundry.org/bbs/cmd/bbs", "-race")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["locket"], err = gexec.Build("code.cloudfoundry.org/locket/cmd/locket", "-race")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["file-server"], err = gexec.Build("code.cloudfoundry.org/fileserver/cmd/file-server", "-race")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["route-emitter"], err = gexec.Build("code.cloudfoundry.org/route-emitter/cmd/route-emitter", "-race")
	Expect(err).NotTo(HaveOccurred())

	Expect(os.Chdir(os.Getenv("ROUTER_GOPATH"))).To(Succeed())
	builtExecutables["router"], err = gexec.Build("code.cloudfoundry.org/gorouter", "-race")
	Expect(err).NotTo(HaveOccurred())
	Expect(os.Chdir(cwd)).To(Succeed())

	builtExecutables["ssh-proxy"], err = gexec.Build("code.cloudfoundry.org/diego-ssh/cmd/ssh-proxy", "-race")
	Expect(err).NotTo(HaveOccurred())

	return builtExecutables
}
