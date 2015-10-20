package docker_test

import (
	"io/ioutil"
	"net"
	"net/url"
	"os"
	"os/exec"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/onsi/gomega/gexec"
	"github.com/onsi/gomega/ghttp"
)

var _ = Describe("Building", func() {
	var (
		builderCmd         *exec.Cmd
		dockerRef          string
		dockerRegistryHost string
		dockerRegistryPort string
		dockerRegistryIPs  string
		outputMetadataDir  string
		fakeDockerRegistry *ghttp.Server
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

	BeforeEach(func() {
		var err error

		dockerRef = "something"

		outputMetadataDir, err = ioutil.TempDir("", "building-result")
		Expect(err).NotTo(HaveOccurred())

		fakeDockerRegistry = ghttp.NewServer()

		dockerRegistryHost = "docker-registry.service.cf.internal"
		dockerRegistryPort = "8080"

		parts, err := url.Parse(fakeDockerRegistry.URL())
		Expect(err).NotTo(HaveOccurred())
		ip, _, err := net.SplitHostPort(parts.Host)
		Expect(err).NotTo(HaveOccurred())
		dockerRegistryIPs = ip
	})

	JustBeforeEach(func() {
		args := []string{}

		args = append(args, "-dockerDaemonExecutablePath", "/bin/true")
		args = append(args, "-dockerRef", dockerRef)
		args = append(args, "-dockerRegistryHost", dockerRegistryHost)
		args = append(args, "-dockerRegistryPort", dockerRegistryPort)
		args = append(args, "-dockerRegistryIPs", dockerRegistryIPs)
		args = append(args, "-cacheDockerImage")

		builderCmd = exec.Command(builderPath, args...)

		builderCmd.Env = os.Environ()
	})

	Context("when running the main", func() {
		var session *gexec.Session

		JustBeforeEach(func() {
			session = setupBuilder()
			Eventually(session.Out, 30).Should(gbytes.Say("Staging process started ..."))
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
