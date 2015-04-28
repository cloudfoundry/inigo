package executor_test

import (
	"encoding/json"
	"os"
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
	"github.com/cloudfoundry-incubator/inigo/world"
)

var (
	componentMaker world.ComponentMaker

	gardenProcess ifrit.Process
	gardenClient  garden.Client
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
	gardenProcess = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
		{"garden-linux", componentMaker.GardenLinux()},
	}))

	gardenClient = componentMaker.GardenClient()
})

var _ = AfterEach(func() {
	destroyContainerErrors := helpers.CleanupGarden(gardenClient)

	helpers.StopProcesses(gardenProcess)

	Expect(destroyContainerErrors).To(
		BeEmpty(),
		"%d containers failed to be destroyed!",
		len(destroyContainerErrors),
	)
})

func TestExecutor(t *testing.T) {
	helpers.RegisterDefaultTimeouts()

	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t, "Executor Integration Suite", []Reporter{
		ginkgoreporter.New(GinkgoWriter),
	})
}

func CompileTestedExecutables() world.BuiltExecutables {
	var err error

	builtExecutables := world.BuiltExecutables{}

	builtExecutables["garden-linux"], err = gexec.BuildIn(os.Getenv("GARDEN_LINUX_GOPATH"), "github.com/cloudfoundry-incubator/garden-linux", "-race", "-a", "-tags", "daemon")
	Expect(err).NotTo(HaveOccurred())

	builtExecutables["exec"], err = gexec.BuildIn(os.Getenv("EXECUTOR_GOPATH"), "github.com/cloudfoundry-incubator/executor/cmd/executor", "-race")
	Expect(err).NotTo(HaveOccurred())

	return builtExecutables
}
