package world

import (
	"io/ioutil"
	"os"

	. "github.com/onsi/gomega"
)

func TempDir(prefix string) string {
	tmpDir, err := ioutil.TempDir(os.TempDir(), prefix)
	Expect(err).NotTo(HaveOccurred())

	err = os.Chmod(tmpDir, 0777)
	Expect(err).NotTo(HaveOccurred())

	return tmpDir
}
