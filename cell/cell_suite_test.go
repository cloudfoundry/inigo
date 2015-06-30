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
	"github.com/pivotal-golang/localip"
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
		Lifecycles:  BuildLifecycles(),
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
	plumbing = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
		{"etcd", componentMaker.Etcd()},
		{"nats", componentMaker.NATS()},
		{"consul", componentMaker.Consul()},
		{"bbs", componentMaker.BBS()},
		{"receptor", componentMaker.Receptor()},
		{"garden-linux", componentMaker.GardenLinux("-denyNetworks=0.0.0.0/0", "-allowHostAccess=true")},
	}))

	helpers.ConsulWaitUntilReady()

	gardenClient = componentMaker.GardenClient()
	natsClient = componentMaker.NATSClient()
	receptorClient = componentMaker.ReceptorClient()

	helpers.UpsertInigoDomain(receptorClient)

	inigo_announcement_server.Start(componentMaker.ExternalAddress)
})

var _ = AfterEach(func() {
	inigo_announcement_server.Stop()

	destroyContainerErrors := helpers.CleanupGarden(gardenClient)

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

	RunSpecsWithDefaultAndCustomReporters(t, "Cell Integration Suite", []Reporter{
		ginkgoreporter.New(GinkgoWriter),
	})
}

func CompileTestedExecutables() world.BuiltExecutables {
	var err error

	builtExecutables := world.BuiltExecutables{}

	builtExecutables["garden-linux"], err = gexec.BuildIn(os.Getenv("GARDEN_LINUX_GOPATH"), "github.com/cloudfoundry-incubator/garden-linux", "-race", "-a", "-tags", "daemon")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["auctioneer"], err = gexec.BuildIn(os.Getenv("AUCTIONEER_GOPATH"), "github.com/cloudfoundry-incubator/auctioneer/cmd/auctioneer", "-race")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["converger"], err = gexec.BuildIn(os.Getenv("CONVERGER_GOPATH"), "github.com/cloudfoundry-incubator/converger/cmd/converger", "-race")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["rep"], err = gexec.BuildIn(os.Getenv("REP_GOPATH"), "github.com/cloudfoundry-incubator/rep/cmd/rep", "-race")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["stager"], err = gexec.BuildIn(os.Getenv("STAGER_GOPATH"), "github.com/cloudfoundry-incubator/stager/cmd/stager", "-race")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["bbs"], err = gexec.BuildIn(os.Getenv("BBS_GOPATH"), "github.com/cloudfoundry-incubator/bbs/cmd/bbs", "-race")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["receptor"], err = gexec.BuildIn(os.Getenv("RECEPTOR_GOPATH"), "github.com/cloudfoundry-incubator/receptor/cmd/receptor", "-race")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["nsync-listener"], err = gexec.BuildIn(os.Getenv("NSYNC_GOPATH"), "github.com/cloudfoundry-incubator/nsync/cmd/nsync-listener", "-race")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["nsync-bulker"], err = gexec.BuildIn(os.Getenv("NSYNC_GOPATH"), "github.com/cloudfoundry-incubator/nsync/cmd/nsync-bulker", "-race")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["file-server"], err = gexec.BuildIn(os.Getenv("FILE_SERVER_GOPATH"), "github.com/cloudfoundry-incubator/file-server/cmd/file-server", "-race")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["route-emitter"], err = gexec.BuildIn(os.Getenv("ROUTE_EMITTER_GOPATH"), "github.com/cloudfoundry-incubator/route-emitter/cmd/route-emitter", "-race")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["tps-listener"], err = gexec.BuildIn(os.Getenv("TPS_GOPATH"), "github.com/cloudfoundry-incubator/tps/cmd/tps-listener", "-race")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["router"], err = gexec.BuildIn(os.Getenv("ROUTER_GOPATH"), "github.com/cloudfoundry/gorouter", "-race")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["ssh-proxy"], err = gexec.Build("github.com/cloudfoundry-incubator/diego-ssh/cmd/ssh-proxy", "-race")
	Expect(err).NotTo(HaveOccurred())

	os.Setenv("CGO_ENABLED", "0")
	builtExecutables["sshd"], err = gexec.Build("github.com/cloudfoundry-incubator/diego-ssh/cmd/sshd", "-a", "-installsuffix", "static")
	os.Unsetenv("CGO_ENABLED")
	Expect(err).NotTo(HaveOccurred())

	return builtExecutables
}

func BuildLifecycles() world.BuiltLifecycles {
	builtLifecycles := world.BuiltLifecycles{}

	builderPath, err := gexec.BuildIn(os.Getenv("BUILDPACK_APP_LIFECYCLE_GOPATH"), "github.com/cloudfoundry-incubator/buildpack_app_lifecycle/builder", "-race")
	Expect(err).NotTo(HaveOccurred())

	launcherPath, err := gexec.BuildIn(os.Getenv("BUILDPACK_APP_LIFECYCLE_GOPATH"), "github.com/cloudfoundry-incubator/buildpack_app_lifecycle/launcher", "-race")
	Expect(err).NotTo(HaveOccurred())

	healthcheckPath, err := gexec.Build("github.com/cloudfoundry-incubator/healthcheck/cmd/healthcheck", "-race")
	Expect(err).NotTo(HaveOccurred())

	lifecycleDir, err := ioutil.TempDir("", "lifecycle-dir")
	Expect(err).NotTo(HaveOccurred())

	err = os.Rename(builderPath, filepath.Join(lifecycleDir, "builder"))
	Expect(err).NotTo(HaveOccurred())

	err = os.Rename(healthcheckPath, filepath.Join(lifecycleDir, "healthcheck"))
	Expect(err).NotTo(HaveOccurred())

	err = os.Rename(launcherPath, filepath.Join(lifecycleDir, "launcher"))
	Expect(err).NotTo(HaveOccurred())

	cmd := exec.Command("tar", "-czf", "lifecycle.tar.gz", "builder", "launcher", "healthcheck")
	cmd.Stderr = GinkgoWriter
	cmd.Stdout = GinkgoWriter
	cmd.Dir = lifecycleDir
	err = cmd.Run()
	Expect(err).NotTo(HaveOccurred())

	for _, stack := range helpers.PreloadedStacks {
		builtLifecycles[stack] = filepath.Join(lifecycleDir, "lifecycle.tar.gz")
	}

	return builtLifecycles
}
