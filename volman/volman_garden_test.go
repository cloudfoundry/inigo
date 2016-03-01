package volman_test

import (
	"io"
	"os"
	"path/filepath"

	"github.com/cloudfoundry-incubator/garden"
	"github.com/cloudfoundry-incubator/volman"
	"github.com/cloudfoundry-incubator/volman/vollocal"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"
	"github.com/pivotal-golang/lager"
	"github.com/pivotal-golang/lager/lagertest"
)

var _ = Describe("Given garden and volman", func() {

	var (
		volmanClient        volman.Manager
		gardenContainerSpec garden.ContainerSpec
		logger              lager.Logger
	)

	BeforeEach(func() {
		logger = lagertest.NewTestLogger("VolmanInigoTest")
		volmanClient = vollocal.NewLocalClient(fakeDriverPath)
	})

	Context("and given a volman mounted volume", func() {

		var mountPoint string

		BeforeEach(func() {
			var err error

			mountPointResponse, err := volmanClient.Mount(logger, "fakedriver", "someVolume", "someconfig")
			Expect(err).NotTo(HaveOccurred())
			mountPoint = mountPointResponse.Path
		})

		It("then garden can create a container with a host origin bind-mount and use it", func() {
			bindMount := garden.BindMount{
				SrcPath: mountPoint,
				DstPath: "/mnt/testmount",
				Mode:    garden.BindMountModeRW,
				Origin:  garden.BindMountOriginHost,
			}

			mounts := []garden.BindMount{}
			mounts = append(mounts, bindMount)
			gardenContainerSpec = garden.ContainerSpec{
				Privileged: true,
				BindMounts: mounts,
			}

			container, err := gardenClient.Create(gardenContainerSpec)
			Expect(err).NotTo(HaveOccurred())

			dir := "/mnt/testmount"
			fileName := "bind-mount-test-file"
			filePath := filepath.Join(dir, fileName)
			createContainerTestFileIn(container, filePath)

			// then it is available inside the container
			out := gbytes.NewBuffer()
			proc, err := container.Run(garden.ProcessSpec{
				User: "root",
				Path: "ls",
				Args: []string{filePath},
			}, garden.ProcessIO{
				Stdout: io.MultiWriter(out, GinkgoWriter),
				Stderr: GinkgoWriter,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(proc.Wait()).To(Equal(0))
			out.Close()

			// and available outside of the container
			files, err := filepath.Glob(mountPoint + "/*")
			Expect(err).ToNot(HaveOccurred())
			Expect(len(files)).To(Equal(1))
			Expect(files[0]).To(Equal(mountPoint + "/" + fileName))
		})
	})
})

func createContainerTestFileIn(container garden.Container, filePath string) {

	process, err := container.Run(garden.ProcessSpec{
		Path: "touch",
		Args: []string{filePath},
		User: "root",
	}, garden.ProcessIO{nil, os.Stdout, os.Stderr})
	Expect(err).ToNot(HaveOccurred())
	Expect(process.Wait()).To(Equal(0))

	process, err = container.Run(garden.ProcessSpec{
		Path: "chmod",
		Args: []string{"0777", filePath},
		User: "root",
	}, garden.ProcessIO{nil, os.Stdout, os.Stderr})
	Expect(err).ToNot(HaveOccurred())
	Expect(process.Wait()).To(Equal(0))

}
