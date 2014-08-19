package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"
)

type VCAPApplicationEnv struct {
	InstanceIndex int `json:"instance_index"`
}

var listenAddr = flag.String(
	"listenAddr",
	":8080",
	"address to listen on",
)

func main() {
	time.Sleep(time.Second)

	var env VCAPApplicationEnv
	err := json.Unmarshal([]byte(os.Getenv("VCAP_APPLICATION")), &env)
	if err != nil {
		panic("Invalid $VCAP_APPLICATION: " + err.Error())
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// used to report to tests
		fmt.Fprintf(w, "%d", env.InstanceIndex)
	})

	addr := *listenAddr
	if os.Getenv("PORT") != "" {
		addr = ":" + os.Getenv("PORT")
	}

	// used to synchronize with tests
	fmt.Printf("Hello World from index '%d'\n", env.InstanceIndex)

	err = http.ListenAndServe(addr, nil)
	if err != nil {
		panic("ListenAndServe: " + err.Error())
	}
}
