package helpers

import (
	"fmt"
	"os"

	"github.com/cloudfoundry-incubator/inigo/world"
	"github.com/onsi/ginkgo/config"

	. "github.com/onsi/gomega"
)

const StackName = "lucid64"

var addresses world.ComponentAddresses

func MakeComponentMaker(builtArtifacts world.BuiltArtifacts, localIP string) world.ComponentMaker {
	gardenBinPath := os.Getenv("GARDEN_BINPATH")
	gardenRootFSPath := os.Getenv("GARDEN_ROOTFS")
	gardenGraphPath := os.Getenv("GARDEN_GRAPH_PATH")
	externalAddress := os.Getenv("EXTERNAL_ADDRESS")

	if gardenGraphPath == "" {
		gardenGraphPath = os.TempDir()
	}

	Ω(gardenBinPath).ShouldNot(BeEmpty(), "must provide $GARDEN_BINPATH")
	Ω(gardenRootFSPath).ShouldNot(BeEmpty(), "must provide $GARDEN_ROOTFS")
	Ω(externalAddress).ShouldNot(BeEmpty(), "must provide $EXTERNAL_ADDRESS")

	addresses = world.ComponentAddresses{
		GardenLinux:         fmt.Sprintf("127.0.0.1:%d", 10000+config.GinkgoConfig.ParallelNode),
		NATS:                fmt.Sprintf("127.0.0.1:%d", 11000+config.GinkgoConfig.ParallelNode),
		Etcd:                fmt.Sprintf("127.0.0.1:%d", 12000+config.GinkgoConfig.ParallelNode),
		EtcdPeer:            fmt.Sprintf("127.0.0.1:%d", 12500+config.GinkgoConfig.ParallelNode),
		Executor:            fmt.Sprintf("127.0.0.1:%d", 13000+config.GinkgoConfig.ParallelNode),
		Rep:                 fmt.Sprintf("0.0.0.0:%d", 14000+config.GinkgoConfig.ParallelNode),
		FileServer:          fmt.Sprintf("%s:%d", localIP, 17000+config.GinkgoConfig.ParallelNode),
		Router:              fmt.Sprintf("127.0.0.1:%d", 18000+config.GinkgoConfig.ParallelNode),
		TPS:                 fmt.Sprintf("127.0.0.1:%d", 19000+config.GinkgoConfig.ParallelNode),
		FakeCC:              fmt.Sprintf("127.0.0.1:%d", 20000+config.GinkgoConfig.ParallelNode),
		Receptor:            fmt.Sprintf("127.0.0.1:%d", 21000+config.GinkgoConfig.ParallelNode),
		ReceptorTaskHandler: fmt.Sprintf("127.0.0.1:%d", 21500+config.GinkgoConfig.ParallelNode),
		Stager:              fmt.Sprintf("127.0.0.1:%d", 22000+config.GinkgoConfig.ParallelNode),
		NsyncListener:       fmt.Sprintf("127.0.0.1:%d", 22500+config.GinkgoConfig.ParallelNode),
		Auctioneer:          fmt.Sprintf("0.0.0.0:%d", 23000+config.GinkgoConfig.ParallelNode),
	}

	return world.ComponentMaker{
		Artifacts: builtArtifacts,
		Addresses: addresses,

		Stack: StackName,

		ExternalAddress: externalAddress,

		GardenBinPath:    gardenBinPath,
		GardenRootFSPath: gardenRootFSPath,
		GardenGraphPath:  gardenGraphPath,
	}
}
