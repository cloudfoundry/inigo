package cell_test

import (
	"os"

	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"

	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/receptor"
	"github.com/cloudfoundry-incubator/runtime-schema/models"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Tasks as specific user", func() {
	var (
		cellProcess ifrit.Process
	)

	var fileServerStaticDir string

	BeforeEach(func() {
		var fileServerRunner ifrit.Runner

		fileServerRunner, fileServerStaticDir = componentMaker.FileServer()

		cellGroup := grouper.Members{
			{"file-server", fileServerRunner},
			{"rep", componentMaker.Rep("-memoryMB", "1024", "-allowPrivileged")},
			{"auctioneer", componentMaker.Auctioneer()},
			{"converger", componentMaker.Converger()},
		}
		cellProcess = ginkgomon.Invoke(grouper.NewParallel(os.Interrupt, cellGroup))

		Eventually(receptorClient.Cells).Should(HaveLen(1))
	})

	AfterEach(func() {
		helpers.StopProcesses(cellProcess)
	})

	Describe("Running a task", func() {
		var guid string

		BeforeEach(func() {
			guid = helpers.GenerateGuid()
		})

		It("runs the command as a specific user", func() {
			taskRequest := helpers.TaskCreateRequest(
				guid,
				models.Serial(
					&models.RunAction{
						User: "root",
						Path: "useradd",
						Args: []string{"-m", "testuser"},
					},
					&models.RunAction{
						User: "testuser",
						Path: "sh",
						Args: []string{"-c", `[ $(whoami) = testuser ]`},
					},
				),
			)
			taskRequest.Privileged = true
			err := receptorClient.CreateTask(taskRequest)
			Expect(err).NotTo(HaveOccurred())

			var task receptor.TaskResponse

			Eventually(func() interface{} {
				var err error

				task, err = receptorClient.GetTask(guid)
				Expect(err).NotTo(HaveOccurred())

				return task.State
			}).Should(Equal(receptor.TaskStateCompleted))
			Expect(task.Failed).To(BeFalse())
		})
	})
})
