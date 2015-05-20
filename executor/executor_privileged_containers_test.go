package executor_test

import (
	"os"

	"github.com/cloudfoundry-incubator/executor"
	executorinit "github.com/cloudfoundry-incubator/executor/initializer"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/nu7hatch/gouuid"
	"github.com/pivotal-golang/lager/lagertest"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Privileged Containers", func() {
	var process ifrit.Process
	var runner ifrit.Runner
	var allowPrivileged bool

	Context("when trying to run a container with a privileged run action", func() {
		var runResult executor.ContainerRunResult

		JustBeforeEach(func() {
			uuid, err := uuid.NewV4()
			Expect(err).NotTo(HaveOccurred())
			containerGuid := uuid.String()

			container := executor.Container{
				Guid: containerGuid,
				Action: &models.RunAction{
					Path:       "sh",
					Args:       []string{"-c", `[ "$(id -u)" -eq "0" ]`},
					Privileged: true,
				},
			}

			config := executorinit.DefaultConfiguration
			config.GardenNetwork = "tcp"
			config.GardenAddr = componentMaker.Addresses.GardenLinux
			config.AllowPrivileged = allowPrivileged
			logger := lagertest.NewTestLogger("test")

			executorClient, executorMembers, err := executorinit.Initialize(logger, config)
			Expect(err).NotTo(HaveOccurred())
			runner = grouper.NewParallel(os.Kill, executorMembers)

			_, err = executorClient.AllocateContainers([]executor.Container{container})
			Expect(err).NotTo(HaveOccurred())

			err = executorClient.RunContainer(containerGuid)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() executor.State {
				container, err := executorClient.GetContainer(containerGuid)
				if err != nil {
					return executor.StateInvalid
				}

				runResult = container.RunResult
				return container.State
			}).Should(Equal(executor.StateCompleted))
		})

		Context("when allowPrivileged is set", func() {
			BeforeEach(func() {
				allowPrivileged = true
			})

			JustBeforeEach(func() {
				process = ginkgomon.Invoke(runner)
			})

			AfterEach(func() {
				ginkgomon.Kill(process)
			})

			It("does not error", func() {
				Expect(runResult.Failed).To(BeFalse())
			})
		})

		Context("when allowPrivileged is not set", func() {
			BeforeEach(func() {
				allowPrivileged = false
			})

			JustBeforeEach(func() {
				process = ginkgomon.Invoke(runner)
			})

			AfterEach(func() {
				ginkgomon.Kill(process)
			})

			It("does error", func() {
				Expect(runResult.Failed).To(BeTrue())
				Expect(runResult.FailureReason).To(Equal("privileged-action-denied"))
			})
		})
	})
})
