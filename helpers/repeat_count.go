package helpers

import (
	"fmt"
	"os"
	"strconv"

	"github.com/onsi/ginkgo"
)

func RepeatCount() int {
	countVar := os.Getenv("INIGO_REPEAT_COUNT")
	if countVar == "" {
		return 1
	}

	count, err := strconv.Atoi(countVar)
	if err != nil {
		ginkgo.Fail(fmt.Sprintf("Invalid INIGO_REPEAT_COUNT env variable - %s", countVar))
	}

	return count
}
