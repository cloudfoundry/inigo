package archiver

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"

	. "github.com/onsi/gomega"
)

type ArchiveFile struct {
	Name string
	Body string
}

func CreateZipArchive(filename string, files []ArchiveFile) {
	file, err := os.Create(filename)
	Ω(err).ShouldNot(HaveOccurred())

	w := zip.NewWriter(file)

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

	err = file.Close()
	Ω(err).ShouldNot(HaveOccurred())
}

func CreateTarGZArchive(filename string, files []ArchiveFile) {
	file, err := os.Create(filename)
	Ω(err).ShouldNot(HaveOccurred())

	gw := gzip.NewWriter(file)
	w := tar.NewWriter(gw)

	for _, file := range files {
		header := &tar.Header{
			Name: file.Name,
			Mode: 0777,
			Size: int64(len(file.Body)),
		}

		err := w.WriteHeader(header)
		Ω(err).ShouldNot(HaveOccurred())

		_, err = w.Write([]byte(file.Body))
		Ω(err).ShouldNot(HaveOccurred())
	}

	err = w.Close()
	Ω(err).ShouldNot(HaveOccurred())

	err = gw.Close()
	Ω(err).ShouldNot(HaveOccurred())

	err = file.Close()
	Ω(err).ShouldNot(HaveOccurred())
}
