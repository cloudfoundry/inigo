package zipper

import (
	"archive/zip"
	"os"

	. "github.com/onsi/gomega"
)

type ZipFile struct {
	Name string
	Body string
}

func CreateZipFile(filename string, files []ZipFile) {
	buf, err := os.Create(filename)
	Ω(err).ShouldNot(HaveOccurred())

	//buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)

	for _, file := range files {
		header := &zip.FileHeader{
			Name: file.Name,
		}
		header.SetMode(0777)
		f, err := w.CreateHeader(header)
		Ω(err).ShouldNot(HaveOccurred())
		_, err = f.Write([]byte(file.Body))
		Ω(err).ShouldNot(HaveOccurred())
	}
	err = w.Close()
	Ω(err).ShouldNot(HaveOccurred())

	err = buf.Close()
	Ω(err).ShouldNot(HaveOccurred())
}
