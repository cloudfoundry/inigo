package executor_test

import (
	"github.com/cloudfoundry-incubator/executor"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	"github.com/nu7hatch/gouuid"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Privileged Containers", func() {
	var process ifrit.Process
	var runner *ginkgomon.Runner

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

			executorClient := componentMaker.ExecutorClient()

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
				runner = componentMaker.Executor("-allowPrivileged", "true")
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
				runner = componentMaker.Executor()
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
