package cell_test

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
	"github.com/pivotal-golang/lager/ginkgoreporter"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"

	"github.com/cloudfoundry-incubator/garden"
	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/inigo/inigo_announcement_server"
	"github.com/cloudfoundry-incubator/inigo/world"
	"github.com/cloudfoundry-incubator/receptor"
	"github.com/cloudfoundry/gunk/diegonats"
)

const INIGO_DOMAIN = "inigo"

var (
	componentMaker world.ComponentMaker

	plumbing       ifrit.Process
	receptorClient receptor.Client
	natsClient     diegonats.NATSClient
	gardenClient   garden.Client
)

var _ = SynchronizedBeforeSuite(func() []byte {
	payload, err := json.Marshal(world.BuiltArtifacts{
		Executables: CompileTestedExecutables(),
		Lifecycles:  CompileAndZipUpLifecycles(),
	})
	Ω(err).ShouldNot(HaveOccurred())

	return payload
}, func(encodedBuiltArtifacts []byte) {
	var builtArtifacts world.BuiltArtifacts

	err := json.Unmarshal(encodedBuiltArtifacts, &builtArtifacts)
	Ω(err).ShouldNot(HaveOccurred())

	componentMaker = helpers.MakeComponentMaker(builtArtifacts)
})

var _ = BeforeEach(func() {
	plumbing = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
		{"etcd", componentMaker.Etcd()},
		{"nats", componentMaker.NATS()},
		{"receptor", componentMaker.Receptor()},
		{"garden-linux", componentMaker.GardenLinux("-denyNetworks=0.0.0.0/0", "-allowHostAccess=true")},
	}))

	gardenClient = componentMaker.GardenClient()
	natsClient = componentMaker.NATSClient()
	receptorClient = componentMaker.ReceptorClient()

	err := receptorClient.UpsertDomain(INIGO_DOMAIN, 0)
	Ω(err).ShouldNot(HaveOccurred())

	inigo_announcement_server.Start(componentMaker.ExternalAddress)
})

var _ = AfterEach(func() {
	inigo_announcement_server.Stop()

	destroyContainerErrors := helpers.CleanupGarden(gardenClient)

	helpers.StopProcesses(plumbing)

	Ω(destroyContainerErrors).Should(
		BeEmpty(),
		"%d containers failed to be destroyed!",
		len(destroyContainerErrors),
	)
})

func TestCell(t *testing.T) {
	helpers.RegisterDefaultTimeouts()

	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t, "Cell Integration Suite", []Reporter{
		ginkgoreporter.New(GinkgoWriter),
	})
}

func CompileTestedExecutables() world.BuiltExecutables {
	var err error

	builtExecutables := world.BuiltExecutables{}

	builtExecutables["garden-linux"], err = gexec.BuildIn(os.Getenv("GARDEN_LINUX_GOPATH"), "github.com/cloudfoundry-incubator/garden-linux", "-race", "-a", "-tags", "daemon")
	Ω(err).ShouldNot(HaveOccurred())

	builtExecutables["auctioneer"], err = gexec.BuildIn(os.Getenv("AUCTIONEER_GOPATH"), "github.com/cloudfoundry-incubator/auctioneer/cmd/auctioneer", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	builtExecutables["exec"], err = gexec.BuildIn(os.Getenv("EXECUTOR_GOPATH"), "github.com/cloudfoundry-incubator/executor/cmd/executor", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	builtExecutables["converger"], err = gexec.BuildIn(os.Getenv("CONVERGER_GOPATH"), "github.com/cloudfoundry-incubator/converger/cmd/converger", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	builtExecutables["rep"], err = gexec.BuildIn(os.Getenv("REP_GOPATH"), "github.com/cloudfoundry-incubator/rep/cmd/rep", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	builtExecutables["stager"], err = gexec.BuildIn(os.Getenv("STAGER_GOPATH"), "github.com/cloudfoundry-incubator/stager/cmd/stager", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	builtExecutables["receptor"], err = gexec.BuildIn(os.Getenv("RECEPTOR_GOPATH"), "github.com/cloudfoundry-incubator/receptor/cmd/receptor", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	builtExecutables["nsync-listener"], err = gexec.BuildIn(os.Getenv("NSYNC_GOPATH"), "github.com/cloudfoundry-incubator/nsync/cmd/nsync-listener", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	builtExecutables["nsync-bulker"], err = gexec.BuildIn(os.Getenv("NSYNC_GOPATH"), "github.com/cloudfoundry-incubator/nsync/cmd/nsync-bulker", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	builtExecutables["file-server"], err = gexec.BuildIn(os.Getenv("FILE_SERVER_GOPATH"), "github.com/cloudfoundry-incubator/file-server/cmd/file-server", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	builtExecutables["route-emitter"], err = gexec.BuildIn(os.Getenv("ROUTE_EMITTER_GOPATH"), "github.com/cloudfoundry-incubator/route-emitter/cmd/route-emitter", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	builtExecutables["tps"], err = gexec.BuildIn(os.Getenv("TPS_GOPATH"), "github.com/cloudfoundry-incubator/tps/cmd/tps", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	builtExecutables["router"], err = gexec.BuildIn(os.Getenv("ROUTER_GOPATH"), "github.com/cloudfoundry/gorouter", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	return builtExecutables
}

func CompileAndZipUpLifecycles() world.BuiltLifecycles {
	builtLifecycles := world.BuiltLifecycles{}

	builderPath, err := gexec.BuildIn(os.Getenv("BUILDPACK_APP_LIFECYCLE_GOPATH"), "github.com/cloudfoundry-incubator/buildpack_app_lifecycle/builder", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	healthcheckPath, err := gexec.BuildIn(os.Getenv("BUILDPACK_APP_LIFECYCLE_GOPATH"), "github.com/cloudfoundry-incubator/buildpack_app_lifecycle/healthcheck", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	launcherPath, err := gexec.BuildIn(os.Getenv("BUILDPACK_APP_LIFECYCLE_GOPATH"), "github.com/cloudfoundry-incubator/buildpack_app_lifecycle/launcher", "-race")
	Ω(err).ShouldNot(HaveOccurred())

	lifecycleDir, err := ioutil.TempDir("", "lifecycle-dir")
	Ω(err).ShouldNot(HaveOccurred())

	err = os.Rename(builderPath, filepath.Join(lifecycleDir, "builder"))
	Ω(err).ShouldNot(HaveOccurred())

	err = os.Rename(healthcheckPath, filepath.Join(lifecycleDir, "healthcheck"))
	Ω(err).ShouldNot(HaveOccurred())

	err = os.Rename(launcherPath, filepath.Join(lifecycleDir, "launcher"))
	Ω(err).ShouldNot(HaveOccurred())

	cmd := exec.Command("zip", "-v", "lifecycle.zip", "builder", "launcher", "healthcheck")
	cmd.Stderr = GinkgoWriter
	cmd.Stdout = GinkgoWriter
	cmd.Dir = lifecycleDir
	err = cmd.Run()
	Ω(err).ShouldNot(HaveOccurred())

	builtLifecycles[helpers.StackName] = filepath.Join(lifecycleDir, "lifecycle.zip")

	return builtLifecycles
}
