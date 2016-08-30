package cell_test

import (
	"os"

	"code.cloudfoundry.org/inigo/helpers"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/ginkgomon"
	"github.com/tedsuo/ifrit/grouper"
)

var _ = Describe("Placement Tags", func() {
	var runtime ifrit.Process

	BeforeEach(func() {
		runtime = ginkgomon.Invoke(grouper.NewParallel(os.Kill, grouper.Members{
			{"rep", componentMaker.Rep("-placementTag=inigo-tag")},
		}))
	})

	AfterEach(func() {
		helpers.StopProcesses(runtime)
	})

	It("advertises placement tags in the cell presence", func() {
		presences, err := bbsClient.Cells(logger)
		Expect(err).NotTo(HaveOccurred())

		Expect(presences).To(HaveLen(1))
		Expect(presences[0].PlacementTags).To(Equal([]string{"inigo-tag"}))
	})
})
