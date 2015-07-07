package docker_test

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"time"

	. "github.com/cloudfoundry-incubator/docker_app_lifecycle/Godeps/_workspace/src/github.com/onsi/ginkgo"
	. "github.com/cloudfoundry-incubator/docker_app_lifecycle/Godeps/_workspace/src/github.com/onsi/gomega"
	"github.com/cloudfoundry-incubator/docker_app_lifecycle/Godeps/_workspace/src/github.com/onsi/gomega/gbytes"
	"github.com/cloudfoundry-incubator/docker_app_lifecycle/Godeps/_workspace/src/github.com/onsi/gomega/gexec"
	"github.com/cloudfoundry-incubator/docker_app_lifecycle/Godeps/_workspace/src/github.com/onsi/gomega/ghttp"
)

var _ = Describe("Building", func() {
	var (
		builderCmd                 *exec.Cmd
		dockerRef                  string
		dockerImageURL             string
		dockerRegistryAddresses    string
		insecureDockerRegistries   string
		dockerDaemonExecutablePath string
		cacheDockerImage           bool
		dockerLoginServer          string
		dockerUser                 string
		dockerPassword             string
		dockerEmail                string
		outputMetadataDir          string
		outputMetadataJSONFilename string
		fakeDockerRegistry         *ghttp.Server
	)

	setupBuilder := func() *gexec.Session {
		session, err := gexec.Start(
			builderCmd,
			GinkgoWriter,
			GinkgoWriter,
		)
		Expect(err).NotTo(HaveOccurred())

		return session
	}

	setupFakeDockerRegistry := func() {
		fakeDockerRegistry.AppendHandlers(
			ghttp.VerifyRequest("GET", "/v1/_ping"),
			ghttp.CombineHandlers(
				ghttp.VerifyRequest("GET", "/v1/repositories/some-repo/images"),
				http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					w.Header().Set("X-Docker-Token", "token-1,token-2")
					w.Write([]byte(`[
                            {"id": "id-1", "checksum": "sha-1"},
                            {"id": "id-2", "checksum": "sha-2"},
                            {"id": "id-3", "checksum": "sha-3"}
                        ]`))
				}),
			),
		)

		fakeDockerRegistry.AppendHandlers(
			ghttp.CombineHandlers(
				ghttp.VerifyRequest("GET", "/v1/repositories/library/some-repo/tags"),
				http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					w.Write([]byte(`{
                            "latest": "id-1",
                            "some-other-tag": "id-2"
                        }`))
				}),
			),
		)
	}

	BeforeEach(func() {
		var err error

		dockerRef = ""
		dockerImageURL = ""
		dockerRegistryAddresses = ""
		insecureDockerRegistries = ""
		dockerDaemonExecutablePath = "/usr/bin/docker"
		cacheDockerImage = true
		dockerLoginServer = ""
		dockerUser = ""
		dockerPassword = ""
		dockerEmail = ""

		outputMetadataDir, err = ioutil.TempDir("", "building-result")
		Expect(err).NotTo(HaveOccurred())

		outputMetadataJSONFilename = path.Join(outputMetadataDir, "result.json")

		fakeDockerRegistry = ghttp.NewServer()
	})

	dockerPidExists := func() bool {
		_, err := os.Stat("/var/run/docker.pid")
		return err == nil
	}

	AfterEach(func() {
		Eventually(dockerPidExists).Should(BeFalse())
		os.RemoveAll(outputMetadataDir)
	})

	JustBeforeEach(func() {
		args := []string{"-dockerDaemonExecutablePath", dockerDaemonExecutablePath,
			"-outputMetadataJSONFilename", outputMetadataJSONFilename}

		if len(dockerImageURL) > 0 {
			args = append(args, "-dockerImageURL", dockerImageURL)
		}
		if len(dockerRef) > 0 {
			args = append(args, "-dockerRef", dockerRef)
		}
		if len(dockerRegistryAddresses) > 0 {
			args = append(args, "-dockerRegistryAddresses", dockerRegistryAddresses)
		}
		if len(insecureDockerRegistries) > 0 {
			args = append(args, "-insecureDockerRegistries", insecureDockerRegistries)
		}
		if cacheDockerImage {
			args = append(args, "-cacheDockerImage")
		}
		if len(dockerLoginServer) > 0 {
			args = append(args, "-dockerLoginServer", dockerLoginServer)
		}
		if len(dockerUser) > 0 {
			args = append(args, "-dockerUser", dockerUser)
		}
		if len(dockerPassword) > 0 {
			args = append(args, "-dockerPassword", dockerPassword)
		}
		if len(dockerEmail) > 0 {
			args = append(args, "-dockerEmail", dockerEmail)
		}

		builderCmd = exec.Command(builderPath, args...)

		builderCmd.Env = os.Environ()
	})

	buildDockerImageURL := func() string {
		parts, err := url.Parse(fakeDockerRegistry.URL())
		Expect(err).NotTo(HaveOccurred())
		return fmt.Sprintf("docker://%s/some-repo", parts.Host)
	}

	Context("when running the main", func() {

		Context("when signalled", func() {
			var session *gexec.Session

			BeforeEach(func() {
				dockerImageURL = buildDockerImageURL()

				parts, err := url.Parse(fakeDockerRegistry.URL())
				Expect(err).NotTo(HaveOccurred())
				dockerRegistryAddresses = parts.Host

				setupFakeDockerRegistry()
				fakeDockerRegistry.AppendHandlers(
					ghttp.CombineHandlers(
						ghttp.VerifyRequest("GET", "/v1/images/id-1/json"),
						http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
							w.Header().Add("X-Docker-Size", "789")
							w.Write([]byte(`{"id":"layer-1","parent":"parent-1","Config":{"Cmd":["-bazbot","-foobar"],"Entrypoint":["/dockerapp","-t"],"WorkingDir":"/workdir"}}`))

							// give the tests time to send signals, while the builder is "working"
							time.Sleep(2 * time.Second)
						}),
					),
				)
			})

			Context("and builder is interrupted", func() {
				JustBeforeEach(func() {
					session = setupBuilder()
					Eventually(session.Out).Should(gbytes.Say("Staging process started ..."))
					session.Interrupt()
				})

				It("processes the signal and exits", func() {
					Eventually(session).Should(gexec.Exit(2))
				})
			})

			Context("and docker is killed", func() {
				JustBeforeEach(func() {
					session = setupBuilder()
					Eventually(session.Out).Should(gbytes.Say("Staging process started ..."))

					cmd := exec.Command("/usr/bin/killall", "docker")
					cmd.Env = os.Environ()
					err := cmd.Run()
					Expect(err).NotTo(HaveOccurred())

					os.Remove("/var/run/docker.sock")
				})

				It("processes the signal and exits", func() {
					Eventually(session).Should(gexec.Exit(2))
				})
			})

		})
	})
})
