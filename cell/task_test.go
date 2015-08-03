package cell_test

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	"github.com/pivotal-golang/archiver/extractor/test_helper"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"

	"github.com/cloudfoundry-incubator/bbs/models"
	"github.com/cloudfoundry-incubator/inigo/helpers"
	"github.com/cloudfoundry-incubator/inigo/inigo_announcement_server"
	"github.com/cloudfoundry-incubator/receptor"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/ghttp"
)

var _ = Describe("Tasks", func() {
	var (
		cellProcess ifrit.Process
	)

	var fileServerStaticDir string

	BeforeEach(func() {
		var fileServerRunner ifrit.Runner

		fileServerRunner, fileServerStaticDir = componentMaker.FileServer()

		cellGroup := grouper.Members{
			{"file-server", fileServerRunner},
			{"rep", componentMaker.Rep("-memoryMB", "1024")},
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

		It("runs the command with the provided environment", func() {
			err := receptorClient.CreateTask(helpers.TaskCreateRequest(
				guid,
				&models.RunAction{
					User: "vcap",
					Path: "sh",
					Args: []string{"-c", `[ "$FOO" = NEW-BAR -a "$BAZ" = WIBBLE ]`},
					Env: []*models.EnvironmentVariable{
						{"FOO", "OLD-BAR"},
						{"BAZ", "WIBBLE"},
						{"FOO", "NEW-BAR"},
					},
				},
			))
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

		It("runs the command with the provided working directory", func() {
			err := receptorClient.CreateTask(helpers.TaskCreateRequest(
				guid,
				&models.RunAction{
					User: "vcap",
					Path: "sh",
					Args: []string{"-c", `[ $PWD = /tmp ]`},
					Dir:  "/tmp",
				},
			))
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

		Context("when the command exceeds its memory limit", func() {
			It("should fail the Task", func() {
				err := receptorClient.CreateTask(helpers.TaskCreateRequestWithMemoryAndDisk(
					guid,
					models.Serial(
						&models.RunAction{
							User: "vcap",
							Path: "curl",
							Args: []string{inigo_announcement_server.AnnounceURL("before-memory-overdose")},
						},
						&models.RunAction{
							User: "vcap",
							Path: "sh",
							Args: []string{"-c", "yes $(yes)"},
						},
						&models.RunAction{
							User: "vcap",
							Path: "curl",
							Args: []string{inigo_announcement_server.AnnounceURL("after-memory-overdose")},
						},
					),
					10,
					1024,
				))
				Expect(err).NotTo(HaveOccurred())

				Eventually(inigo_announcement_server.Announcements).Should(ContainElement("before-memory-overdose"))

				var task receptor.TaskResponse
				Eventually(func() interface{} {
					var err error

					task, err = receptorClient.GetTask(guid)
					Expect(err).NotTo(HaveOccurred())

					return task.State
				}).Should(Equal(receptor.TaskStateCompleted))

				Expect(task.Failed).To(BeTrue())
				Expect(task.FailureReason).To(ContainSubstring("out of memory"))

				Expect(inigo_announcement_server.Announcements()).NotTo(ContainElement("after-memory-overdose"))
			})
		})

		Context("when the command exceeds its file descriptor limit", func() {
			It("should fail the Task", func() {
				nofile := uint64(10)

				err := receptorClient.CreateTask(helpers.TaskCreateRequest(
					guid,
					models.Serial(
						&models.RunAction{
							User: "vcap",
							Path: "sh",
							Args: []string{"-c", `
set -e

# must start after fd 2
exec 3<>file1
exec 4<>file2
exec 5<>file3
exec 6<>file4
exec 7<>file5
exec 8<>file6
exec 9<>file7
exec 10<>file8
exec 11<>file9
exec 12<>file10
exec 13<>file11

echo should have died by now
`},
							ResourceLimits: &models.ResourceLimits{
								Nofile: &nofile,
							},
						},
					),
				))
				Expect(err).NotTo(HaveOccurred())

				var task receptor.TaskResponse
				Eventually(func() interface{} {
					var err error

					task, err = receptorClient.GetTask(guid)
					Expect(err).NotTo(HaveOccurred())

					return task.State
				}).Should(Equal(receptor.TaskStateCompleted))

				Expect(task.Failed).To(BeTrue())

				// when sh can't open another file the exec exits 2
				Expect(task.FailureReason).To(ContainSubstring("status 2"))
			})
		})

		Context("when the command times out", func() {
			It("should fail the Task", func() {
				err := receptorClient.CreateTask(helpers.TaskCreateRequest(
					guid,
					models.Serial(
						models.Timeout(
							&models.RunAction{
								User: "vcap",
								Path: "sleep",
								Args: []string{"1"},
							},
							500*time.Millisecond,
						),
					),
				))
				Expect(err).NotTo(HaveOccurred())

				var task receptor.TaskResponse
				Eventually(func() interface{} {
					var err error

					task, err = receptorClient.GetTask(guid)
					Expect(err).NotTo(HaveOccurred())

					return task.State
				}).Should(Equal(receptor.TaskStateCompleted))

				Expect(task.Failed).To(BeTrue())
				Expect(task.FailureReason).To(ContainSubstring("exceeded 500ms timeout"))
			})
		})
	})
	Describe("Running a downloaded file", func() {
		var guid string

		BeforeEach(func() {
			guid = helpers.GenerateGuid()

			test_helper.CreateTarGZArchive(filepath.Join(fileServerStaticDir, "announce.tar.gz"), []test_helper.ArchiveFile{
				{
					Name: "announce",
					Body: fmt.Sprintf("#!/bin/sh\n\ncurl %s", inigo_announcement_server.AnnounceURL(guid)),
					Mode: 0755,
				},
			})
		})

		It("downloads the file", func() {
			err := receptorClient.CreateTask(helpers.TaskCreateRequest(
				guid,
				models.Serial(
					&models.DownloadAction{
						From: fmt.Sprintf("http://%s/v1/static/%s", componentMaker.Addresses.FileServer, "announce.tar.gz"),
						To:   ".",
						User: "vcap",
					},
					&models.RunAction{
						User: "vcap",
						Path: "./announce",
					},
				),
			))
			Expect(err).NotTo(HaveOccurred())

			Eventually(inigo_announcement_server.Announcements).Should(ContainElement(guid))
		})
	})

	Describe("Uploading from the container", func() {
		var guid string

		var server *httptest.Server
		var uploadAddr string

		var gotRequest chan struct{}

		BeforeEach(func() {
			guid = helpers.GenerateGuid()

			gotRequest = make(chan struct{})

			server, uploadAddr = helpers.Callback(componentMaker.ExternalAddress, ghttp.CombineHandlers(
				ghttp.VerifyRequest("POST", "/thingy"),
				func(w http.ResponseWriter, r *http.Request) {
					contents, err := ioutil.ReadAll(r.Body)
					Expect(err).NotTo(HaveOccurred())

					Expect(string(contents)).To(Equal("tasty thingy\n"))

					close(gotRequest)
				},
			))
		})

		AfterEach(func() {
			server.Close()
		})

		It("uploads the specified files", func() {
			err := receptorClient.CreateTask(helpers.TaskCreateRequest(
				guid,
				models.Serial(
					&models.RunAction{
						User: "vcap",
						Path: "sh",
						Args: []string{"-c", "echo tasty thingy > thingy"},
					},
					&models.UploadAction{
						From: "thingy",
						To:   fmt.Sprintf("http://%s/thingy", uploadAddr),
						User: "vcap",
					},
					&models.RunAction{
						User: "vcap",
						Path: "curl",
						Args: []string{inigo_announcement_server.AnnounceURL(guid)},
					},
				),
			))
			Expect(err).NotTo(HaveOccurred())

			Eventually(gotRequest).Should(BeClosed())

			Eventually(inigo_announcement_server.Announcements).Should(ContainElement(guid))
		})
	})

	Describe("Fetching results", func() {
		It("should fetch the contents of the requested file and provide the content in the completed Task", func() {
			guid := helpers.GenerateGuid()

			taskRequest := helpers.TaskCreateRequest(
				guid,
				&models.RunAction{
					User: "vcap",
					Path: "sh",
					Args: []string{"-c", "echo tasty thingy > thingy"},
				},
			)
			taskRequest.ResultFile = "/home/vcap/thingy"
			err := receptorClient.CreateTask(taskRequest)
			Expect(err).NotTo(HaveOccurred())

			var task receptor.TaskResponse
			Eventually(func() interface{} {
				var err error

				task, err = receptorClient.GetTask(guid)
				Expect(err).NotTo(HaveOccurred())

				return task.State
			}).Should(Equal(receptor.TaskStateCompleted))

			Expect(task.Result).To(Equal("tasty thingy\n"))
		})
	})
})
