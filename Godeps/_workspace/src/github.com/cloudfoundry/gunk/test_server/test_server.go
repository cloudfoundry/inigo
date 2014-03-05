package test_server

import (
	"fmt"
	"github.com/onsi/ginkgo"
	"github.com/onsi/gomega/format"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
)

func New() *Server {
	s := &Server{
		AllowUnhandledRequests:     false,
		UnhandledRequestStatusCode: http.StatusInternalServerError,
	}
	s.HTTPTestServer = httptest.NewServer(s)
	return s
}

func NewTLS() *Server {
	s := &Server{
		AllowUnhandledRequests:     false,
		UnhandledRequestStatusCode: http.StatusInternalServerError,
	}
	s.HTTPTestServer = httptest.NewTLSServer(s)
	return s
}

type Server struct {
	HTTPTestServer *httptest.Server

	ReceivedRequests []*http.Request
	RequestHandlers  []http.HandlerFunc

	AllowUnhandledRequests     bool
	UnhandledRequestStatusCode int

	calls int
}

func (s *Server) URL() string {
	return s.HTTPTestServer.URL
}

func (s *Server) Close() {
	server := s.HTTPTestServer
	s.HTTPTestServer = nil
	server.Close()
}

func (s *Server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if s.calls < len(s.RequestHandlers) {
		s.RequestHandlers[s.calls](w, req)
	} else {
		if s.AllowUnhandledRequests {
			ioutil.ReadAll(req.Body)
			req.Body.Close()
			w.WriteHeader(s.UnhandledRequestStatusCode)
		} else {
			ginkgo.Fail(fmt.Sprintf("Received unhandled request:\n%s", format.Object(req, 1)))
		}
	}
	s.ReceivedRequests = append(s.ReceivedRequests, req)
	s.calls++
}

func (s *Server) Append(handlers ...http.HandlerFunc) {
	s.RequestHandlers = append(s.RequestHandlers, handlers...)
}

func (s *Server) Set(index int, handler http.HandlerFunc) {
	s.RequestHandlers[index] = handler
}

func (s *Server) Get(index int) http.HandlerFunc {
	return s.RequestHandlers[index]
}

func (s *Server) Wrap(index int, handler http.HandlerFunc) {
	existingHandler := s.Get(index)
	s.Set(index, CombineHandlers(existingHandler, handler))
}
