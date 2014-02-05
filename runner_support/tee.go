package runner_support

import (
	"github.com/onsi/ginkgo/config"
	"io"
	"io/ioutil"
	"os"
)

func TeeIfVerbose(out io.Writer) io.Writer {
	if config.DefaultReporterConfig.Verbose {
		return io.MultiWriter(out, os.Stdout)
	} else {
		return io.MultiWriter(out, ioutil.Discard)
	}
}
