package cell_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"code.cloudfoundry.org/consuladapter/consulrunner"
	"code.cloudfoundry.org/durationjson"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/localip"
	. "github.com/onsi/ginkgo"
	ginkgoconfig "github.com/onsi/ginkgo/config"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"

	"code.cloudfoundry.org/bbs"
	bbsconfig "code.cloudfoundry.org/bbs/cmd/bbs/config"
	"code.cloudfoundry.org/bbs/serviceclient"
	"code.cloudfoundry.org/garden"
	"code.cloudfoundry.org/inigo/helpers"
	"code.cloudfoundry.org/inigo/helpers/certauthority"
	"code.cloudfoundry.org/inigo/helpers/portauthority"
	"code.cloudfoundry.org/inigo/inigo_announcement_server"
	"code.cloudfoundry.org/inigo/world"
)

var (
	componentMaker world.ComponentMaker

	plumbing, bbsProcess, gardenProcess ifrit.Process
	gardenClient                        garden.Client
	bbsClient                           bbs.InternalClient
	bbsServiceClient                    serviceclient.ServiceClient
	lgr                                 lager.Logger
	suiteTempDir                        string
)

func overrideConvergenceRepeatInterval(conf *bbsconfig.BBSConfig) {
	conf.ConvergeRepeatInterval = durationjson.Duration(time.Second)
}

var _ = SynchronizedBeforeSuite(func() []byte {
	suiteTempDir = world.TempDir("before-suite")
	artifacts := world.BuiltArtifacts{
		Lifecycles: world.BuiltLifecycles{},
	}

	artifacts.Lifecycles.BuildLifecycles("dockerapplifecycle", suiteTempDir)
	artifacts.Executables = CompileTestedExecutables()
	artifacts.Healthcheck = CompileHealthcheckExecutable(suiteTempDir)

	payload, err := json.Marshal(artifacts)
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
		Garden:              fmt.Sprintf("127.0.0.1:%d", 10000+ginkgoconfig.GinkgoConfig.ParallelNode),
		NATS:                fmt.Sprintf("127.0.0.1:%d", 11000+ginkgoconfig.GinkgoConfig.ParallelNode),
		Consul:              fmt.Sprintf("127.0.0.1:%d", 12750+ginkgoconfig.GinkgoConfig.ParallelNode*consulrunner.PortOffsetLength),
		Rep:                 fmt.Sprintf("127.0.0.1:%d", 14000+ginkgoconfig.GinkgoConfig.ParallelNode),
		FileServer:          fmt.Sprintf("%s:%d", localIP, 17000+ginkgoconfig.GinkgoConfig.ParallelNode),
		Router:              fmt.Sprintf("127.0.0.1:%d", 18000+ginkgoconfig.GinkgoConfig.ParallelNode),
		BBS:                 fmt.Sprintf("127.0.0.1:%d", 20500+ginkgoconfig.GinkgoConfig.ParallelNode*2),
		Health:              fmt.Sprintf("127.0.0.1:%d", 20500+ginkgoconfig.GinkgoConfig.ParallelNode*2+1),
		Auctioneer:          fmt.Sprintf("127.0.0.1:%d", 23000+ginkgoconfig.GinkgoConfig.ParallelNode),
		SSHProxy:            fmt.Sprintf("127.0.0.1:%d", 23500+ginkgoconfig.GinkgoConfig.ParallelNode),
		SSHProxyHealthCheck: fmt.Sprintf("127.0.0.1:%d", 24500+ginkgoconfig.GinkgoConfig.ParallelNode),
		FakeVolmanDriver:    fmt.Sprintf("127.0.0.1:%d", 25500+ginkgoconfig.GinkgoConfig.ParallelNode),
		Locket:              fmt.Sprintf("127.0.0.1:%d", 26500+ginkgoconfig.GinkgoConfig.ParallelNode),
		SQL:                 fmt.Sprintf("%sdiego_%d", dbBaseConnectionString, ginkgoconfig.GinkgoConfig.ParallelNode),
	}

	node := GinkgoParallelNode()
	startPort := 1000 * node
	portRange := 950
	endPort := startPort + portRange

	allocator, err := portauthority.New(startPort, endPort)
	Expect(err).NotTo(HaveOccurred())

	certDepot := world.TempDirWithParent(suiteTempDir, "cert-depot")

	certAuthority, err := certauthority.NewCertAuthority(certDepot, "ca")
	Expect(err).NotTo(HaveOccurred())

	componentMaker = world.MakeComponentMaker(builtArtifacts, addresses, allocator, certAuthority)
	componentMaker.Setup()
})

var _ = AfterSuite(func() {
	if componentMaker != nil {
		componentMaker.Teardown()
	}

	deleteSuiteTempDir := func() error { return os.RemoveAll(suiteTempDir) }
	Eventually(deleteSuiteTempDir).Should(Succeed())
})

var _ = BeforeEach(func() {
	plumbing = ginkgomon.Invoke(grouper.NewOrdered(os.Kill, grouper.Members{
		{"initial-services", grouper.NewParallel(os.Kill, grouper.Members{
			{"sql", componentMaker.SQL()},
			{"nats", componentMaker.NATS()},
			{"consul", componentMaker.Consul()},
		})},
		{"locket", componentMaker.Locket()},
	}))
	gardenProcess = ginkgomon.Invoke(componentMaker.Garden())
	bbsProcess = ginkgomon.Invoke(componentMaker.BBS())

	helpers.ConsulWaitUntilReady(componentMaker.Addresses())
	lgr = lager.NewLogger("test")
	lgr.RegisterSink(lager.NewWriterSink(GinkgoWriter, lager.DEBUG))

	gardenClient = componentMaker.GardenClient()
	bbsClient = componentMaker.BBSClient()
	bbsServiceClient = componentMaker.BBSServiceClient(lgr)

	inigo_announcement_server.Start(os.Getenv("EXTERNAL_ADDRESS"))
})

var _ = AfterEach(func() {
	inigo_announcement_server.Stop()

	destroyContainerErrors := helpers.CleanupGarden(gardenClient)

	helpers.StopProcesses(bbsProcess)
	helpers.StopProcesses(gardenProcess)
	helpers.StopProcesses(plumbing)

	Expect(destroyContainerErrors).To(
		BeEmpty(),
		"%d containers failed to be destroyed!",
		len(destroyContainerErrors),
	)
})

func TestCell(t *testing.T) {
	helpers.RegisterDefaultTimeouts()

	RegisterFailHandler(Fail)

	RunSpecs(t, "Cell Integration Suite")
}

func CompileHealthcheckExecutable(tmpDir string) string {
	healthcheckDir := world.TempDirWithParent(tmpDir, "healthcheck")
	healthcheckPath, err := gexec.Build("code.cloudfoundry.org/healthcheck/cmd/healthcheck", "-race")
	Expect(err).NotTo(HaveOccurred())

	err = os.Rename(healthcheckPath, filepath.Join(healthcheckDir, "healthcheck"))
	Expect(err).NotTo(HaveOccurred())

	return healthcheckDir
}

func CompileTestedExecutables() world.BuiltExecutables {
	var err error

	builtExecutables := world.BuiltExecutables{}

	builtExecutables["garden"], err = gexec.Build("code.cloudfoundry.org/guardian/cmd/gdn", "-race", "-a", "-tags", "daemon")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["auctioneer"], err = gexec.BuildIn(os.Getenv("AUCTIONEER_GOPATH"), "code.cloudfoundry.org/auctioneer/cmd/auctioneer", "-race")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["rep"], err = gexec.BuildIn(os.Getenv("REP_GOPATH"), "code.cloudfoundry.org/rep/cmd/rep", "-race")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["bbs"], err = gexec.BuildIn(os.Getenv("BBS_GOPATH"), "code.cloudfoundry.org/bbs/cmd/bbs", "-race")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["locket"], err = gexec.BuildIn(os.Getenv("LOCKET_GOPATH"), "code.cloudfoundry.org/locket/cmd/locket", "-race")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["file-server"], err = gexec.BuildIn(os.Getenv("FILE_SERVER_GOPATH"), "code.cloudfoundry.org/fileserver/cmd/file-server", "-race")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["route-emitter"], err = gexec.BuildIn(os.Getenv("ROUTE_EMITTER_GOPATH"), "code.cloudfoundry.org/route-emitter/cmd/route-emitter", "-race")
	Expect(err).NotTo(HaveOccurred())

	if runtime.GOOS != "windows" {
		builtExecutables["router"], err = gexec.BuildIn(os.Getenv("ROUTER_GOPATH"), "code.cloudfoundry.org/gorouter", "-race")
		Expect(err).NotTo(HaveOccurred())
	}

	builtExecutables["routing-api"], err = gexec.BuildIn(os.Getenv("ROUTING_API_GOPATH"), "code.cloudfoundry.org/routing-api/cmd/routing-api", "-race")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["ssh-proxy"], err = gexec.Build("code.cloudfoundry.org/diego-ssh/cmd/ssh-proxy", "-race")
	Expect(err).NotTo(HaveOccurred())

	os.Setenv("CGO_ENABLED", "0")
	builtExecutables["sshd"], err = gexec.Build("code.cloudfoundry.org/diego-ssh/cmd/sshd", "-a", "-installsuffix", "static")
	os.Unsetenv("CGO_ENABLED")
	Expect(err).NotTo(HaveOccurred())

	return builtExecutables
}
