package httpserver

import "net/http"

type Server struct{ srv *http.Server }

func New() *Server { return &Server{srv: &http.Server{Addr: ":8080"}} }

func (s *Server) Start() error {
	return s.srv.ListenAndServe()
}
