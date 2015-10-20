package docker_test

import (
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gexec"
	"github.com/onsi/gomega/ghttp"
)

var _ = Describe("Building", func() {
	var (
		builderCmd                 *exec.Cmd
		dockerRef                  string
		dockerImageURL             string
		dockerRegistryHost         string
		dockerRegistryPort         string
		dockerRegistryIPs          string
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
		dockerRegistryHost = ""
		dockerRegistryPort = ""
		dockerRegistryIPs = ""
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

	JustBeforeEach(func() {
		args := []string{"-dockerDaemonExecutablePath", dockerDaemonExecutablePath,
			"-outputMetadataJSONFilename", outputMetadataJSONFilename}

		if len(dockerImageURL) > 0 {
			args = append(args, "-dockerImageURL", dockerImageURL)
		}
		if len(dockerRef) > 0 {
			args = append(args, "-dockerRef", dockerRef)
		}
		if len(dockerRegistryHost) > 0 {
			args = append(args, "-dockerRegistryHost", dockerRegistryHost)
		}
		if len(dockerRegistryPort) > 0 {
			args = append(args, "-dockerRegistryPort", dockerRegistryPort)
		}
		if len(dockerRegistryIPs) > 0 {
			args = append(args, "-dockerRegistryIPs", dockerRegistryIPs)
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

	dockerPidExists := func() bool {
		_, err := os.Stat("/var/run/docker.pid")
		return err == nil
	}

	Context("when running the main", func() {
		var session *gexec.Session

		BeforeEach(func() {
			dockerImageURL = buildDockerImageURL()

			dockerRegistryHost = "docker-registry.service.cf.internal"
			dockerRegistryPort = "8080"

			parts, err := url.Parse(fakeDockerRegistry.URL())
			Expect(err).NotTo(HaveOccurred())
			ip, _, err := net.SplitHostPort(parts.Host)
			Expect(err).NotTo(HaveOccurred())
			dockerRegistryIPs = ip

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

		JustBeforeEach(func() {
			session = setupBuilder()
			Eventually(session.Out, 30).Should(gbytes.Say("Staging process started ..."))
		})

		AfterEach(func() {
			Eventually(dockerPidExists).Should(BeFalse())
			os.RemoveAll(outputMetadataDir)
		})

		Context("when signalled", func() {
			Context("and builder is interrupted", func() {
				JustBeforeEach(func() {
					session.Interrupt()
				})

				It("processes the signal and exits", func() {
					Eventually(session).Should(gexec.Exit(2))
				})
			})

			Context("and docker is killed", func() {
				JustBeforeEach(func() {
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

		Context("when started", func() {
			AfterEach(func() {
				session.Interrupt()
				Eventually(session).Should(gexec.Exit(2))
			})

			It("creates /etc/hosts entry", func() {
				hostsContent, err := ioutil.ReadFile("/etc/hosts")
				Expect(err).NotTo(HaveOccurred())

				Expect(hostsContent).To(ContainSubstring("%s %s\n", dockerRegistryIPs, dockerRegistryHost))
			})
		})
	})
})
