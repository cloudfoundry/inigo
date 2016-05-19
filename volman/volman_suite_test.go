package volman_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/cloudfoundry-incubator/garden"
	"github.com/cloudfoundry-incubator/inigo/gardenrunner"
	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/inigo/world"
	"github.com/cloudfoundry-incubator/volman"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
	"github.com/pivotal-golang/lager"
	"github.com/pivotal-golang/lager/ginkgoreporter"
	"github.com/pivotal-golang/lager/lagertest"
	"github.com/pivotal-golang/localip"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
)

var (
	componentMaker world.ComponentMaker

	gardenProcess ifrit.Process
	gardenClient  garden.Client

	fakeDriverDir       string
	volmanClient        volman.Manager
	driverSyncer        ifrit.Runner
	driverSyncerProcess ifrit.Process
	fakedriverProcess   ifrit.Process

	logger lager.Logger
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

	localIP, err := localip.LocalIP()
	Expect(err).NotTo(HaveOccurred())

	componentMaker = helpers.MakeComponentMaker(builtArtifacts, localIP)
})

var _ = BeforeEach(func() {
	logger = lagertest.NewTestLogger("volman-inigo-suite")

	gardenProcess = ginkgomon.Invoke(componentMaker.Garden())
	gardenClient = componentMaker.GardenClient()

	fakedriverProcess = ginkgomon.Invoke(componentMaker.VolmanDriver(logger))

	volmanClient, driverSyncer = componentMaker.VolmanClient(logger)
	driverSyncerProcess = ginkgomon.Invoke(driverSyncer)
})

var _ = AfterEach(func() {
	destroyContainerErrors := helpers.CleanupGarden(gardenClient)

	helpers.StopProcesses(gardenProcess, driverSyncerProcess, fakedriverProcess)

	Expect(destroyContainerErrors).To(
		BeEmpty(),
		"%d containers failed to be destroyed!",
		len(destroyContainerErrors),
	)
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

	builtExecutables := world.BuiltExecutables{}

	builtExecutables["garden"], err = gexec.BuildIn(os.Getenv("GARDEN_GOPATH"), gardenrunner.GardenServerPackageName(), "-race", "-a", "-tags", "daemon")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["fake-driver"], err = gexec.Build("github.com/cloudfoundry-incubator/volman/fakedriver/cmd/fakedriver", "-race")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["auctioneer"], err = gexec.BuildIn(os.Getenv("AUCTIONEER_GOPATH"), "github.com/cloudfoundry-incubator/auctioneer/cmd/auctioneer", "-race")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["converger"], err = gexec.BuildIn(os.Getenv("CONVERGER_GOPATH"), "github.com/cloudfoundry-incubator/converger/cmd/converger", "-race")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["rep"], err = gexec.BuildIn(os.Getenv("REP_GOPATH"), "github.com/cloudfoundry-incubator/rep/cmd/rep", "-race")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["bbs"], err = gexec.BuildIn(os.Getenv("BBS_GOPATH"), "github.com/cloudfoundry-incubator/bbs/cmd/bbs", "-race")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["file-server"], err = gexec.BuildIn(os.Getenv("FILE_SERVER_GOPATH"), "github.com/cloudfoundry-incubator/file-server/cmd/file-server", "-race")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["route-emitter"], err = gexec.BuildIn(os.Getenv("ROUTE_EMITTER_GOPATH"), "github.com/cloudfoundry-incubator/route-emitter/cmd/route-emitter", "-race")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["router"], err = gexec.BuildIn(os.Getenv("ROUTER_GOPATH"), "github.com/cloudfoundry/gorouter", "-race")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["ssh-proxy"], err = gexec.Build("github.com/cloudfoundry-incubator/diego-ssh/cmd/ssh-proxy", "-race")
	Expect(err).NotTo(HaveOccurred())

	return builtExecutables
}
