package main

import (
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	dir      = "/data"
	hostport = ":8080"
)

func main() {
	info, err := os.Stat(dir)
	handleError("could not stat data dir", err)

	if info.IsDir() {
		log.Println("successfully found data dir!")
	} else {
		log.Fatalln("data dir is not a dir?")
	}

	http.HandleFunc("/get/", func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.URL.Path, "/get/")
		log.Println(r.URL.Path, "getting", key)

		file, err := os.Open(filepath.Join(dir, key))
		handleError("could not open file", err)
		defer closeFile(file)

		_, err = io.Copy(w, file)
		handleError("failed to stream response", err)
		log.Println(r.URL.Path, "getting", key, "succeeded")
	})

	http.HandleFunc("/set/", func(w http.ResponseWriter, r *http.Request) {
		key, value := filepath.Split(strings.TrimPrefix(r.URL.Path, "/set/"))
		log.Println(r.URL.Path, "setting", key, value)

		file, err := os.Create(filepath.Join(dir, key))
		handleError("could not create file", err)
		defer closeFile(file)

		_, err = file.Write([]byte(value))
		handleError("failed to write file", err)
		log.Println(r.URL.Path, "setting", key, value, "succeeded")
	})

	http.HandleFunc("/die", func(w http.ResponseWriter, r *http.Request) {
		log.Println("Shutdown Requested")
		os.Exit(2)
	})

	log.Println("listening on ", hostport)
	err = http.ListenAndServe(hostport, nil)
	handleError("server exited", err)
}

func handleError(msg string, err error) {
	if err != nil {
		log.Panicln(msg, ":", err)
	}
}

func closeFile(file *os.File) {
	handleError("failed to close file", file.Close())
}
