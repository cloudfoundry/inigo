package helpers

import (
	"io/ioutil"
	"net/http"
	"net/url"
	"sort"

	. "github.com/onsi/gomega"
)

func ResponseCodeFromHostPoller(routerAddr string, host string) func() int {
	return func() int {
		request := &http.Request{
			URL: &url.URL{
				Scheme: "http",
				Host:   routerAddr,
				Path:   "/",
			},

			Host: host,
		}

		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return 0
		}
		defer response.Body.Close()

		return response.StatusCode
	}
}

func ResponseBodyAndStatusCodeFromHost(routerAddr string, host string) ([]byte, int, error) {
	request := &http.Request{
		URL: &url.URL{
			Scheme: "http",
			Host:   routerAddr,
			Path:   "/",
		},

		Host: host,
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()

	contents, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return nil, 0, err
	}

	return contents, response.StatusCode, nil
}

func HelloWorldInstancePoller(routerAddr, host string) func() []string {
	return func() []string {
		respondingIndicesHash := map[string]bool{}
		for i := 0; i < 20; i++ {
			body, status, err := ResponseBodyAndStatusCodeFromHost(routerAddr, host)
			if err != nil {
				continue
			}
			if status == http.StatusNotFound {
				//Ignore 404s as they are coming from the router, but make sure...
				Ω(body).Should(MatchRegexp(`Requested route \('.*'\) does not exist`), "Got a 404, but it wasn't from the router!")
				continue
			}
			if status == http.StatusBadGateway {
				//Ignore 502s as they are coming from the router, but make sure...
				Ω(body).Should(ContainSubstring("Registered endpoint failed to handle the request"), "Got a 502, but it wasn't from the router!")
				continue
			}
			respondingIndicesHash[string(body)] = true
		}
		respondingIndices := []string{}
		for key := range respondingIndicesHash {
			respondingIndices = append(respondingIndices, key)
		}
		sort.StringSlice(respondingIndices).Sort()
		return respondingIndices
	}
}
