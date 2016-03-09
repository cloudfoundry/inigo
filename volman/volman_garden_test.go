package volman_test

import (
	"os"

	"path/filepath"

	"github.com/cloudfoundry-incubator/garden"
	"github.com/onsi/gomega/gbytes"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Given garden and a mounted volume", func() {

	var (
		// volmanClient        volman.Manager
		mountPoint string
	)

	BeforeEach(func() {

		var err error
		mountPointResponse, err := volmanClient.Mount(logger, "fakedriver", "someVolume", "someconfig")
		Expect(err).NotTo(HaveOccurred())
		mountPoint = mountPointResponse.Path
	})

	Context("and a container with a host origin bind-mount", func() {

		var bindMount garden.BindMount
		var container garden.Container

		BeforeEach(func() {

			bindMount = garden.BindMount{
				SrcPath: mountPoint,
				DstPath: "/mnt/testmount",
				Mode:    garden.BindMountModeRW,
				Origin:  garden.BindMountOriginHost,
			}
		})

		JustBeforeEach(func() {
			container = createContainer(bindMount)
		})

		It("the container should be able to write to that volume", func() {

			dir := "/mnt/testmount"
			fileName := "bind-mount-test-file"
			filePath := filepath.Join(dir, fileName)
			createContainerTestFileIn(container, filePath)

			expectContainerTestFileExists(container, filePath)

			// and expect it is available outside of the container
			{
				files, err := filepath.Glob(mountPoint + "/*")
				Expect(err).ToNot(HaveOccurred())
				Expect(len(files)).To(Equal(1))
				Expect(files[0]).To(Equal(mountPoint + "/" + fileName))
			}
		})

		Context("and a second container with the same host origin bind-mount", func() {

			var bindMount2 garden.BindMount
			var container2 garden.Container

			BeforeEach(func() {
				bindMount2 = garden.BindMount{
					SrcPath: mountPoint,
					DstPath: "/mnt/testmount2",
					Mode:    garden.BindMountModeRW,
					Origin:  garden.BindMountOriginHost,
				}
			})

			JustBeforeEach(func() {
				container2 = createContainer(bindMount2)
			})

			It("the second container should be able to access data written by the first", func() {

				dir := "/mnt/testmount"
				fileName := "bind-mount-test-file"
				filePath := filepath.Join(dir, fileName)
				createContainerTestFileIn(container, filePath)
				expectContainerTestFileExists(container, filePath)

				dir = "/mnt/testmount2"
				filePath = filepath.Join(dir, fileName)
				expectContainerTestFileExists(container2, filePath)
			})
		})
	})
})

func createContainer(bindMount garden.BindMount) garden.Container {
	mounts := []garden.BindMount{}
	mounts = append(mounts, bindMount)
	gardenContainerSpec := garden.ContainerSpec{
		Privileged: true,
		BindMounts: mounts,
	}

	container, err := gardenClient.Create(gardenContainerSpec)
	Expect(err).NotTo(HaveOccurred())

	return container
}

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

func expectContainerTestFileExists(container garden.Container, filePath string) {
	out := gbytes.NewBuffer()
	defer out.Close()

	proc, err := container.Run(garden.ProcessSpec{
		Path: "ls",
		Args: []string{filePath},
		User: "root",
	}, garden.ProcessIO{nil, out, out})
	Expect(err).NotTo(HaveOccurred())
	Expect(proc.Wait()).To(Equal(0))
	Expect(out).To(gbytes.Say(filePath))
}
