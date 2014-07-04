package match_array_or_slice_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"testing"
)

func TestMatchArrayOrSlice(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "MatchArrayOrSlice Suite")
}
