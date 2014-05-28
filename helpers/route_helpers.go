package helpers

import (
	"io/ioutil"
	"net/http"
	"net/url"
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

func ResponseBodyFromHost(routerAddr string, host string) ([]byte, error) {
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
		return nil, err
	}
	defer response.Body.Close()

	contents, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}

	return contents, nil
}
