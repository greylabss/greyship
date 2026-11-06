package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"greyship.tech/internal/build/fly"
)

// BuildReports is the side of the API that build machines talk to (decisions.md 039).
type BuildReports interface {
	UploadURL(ctx context.Context, token, deployment string, req fly.UploadRequest) (string, error)
	Add(ctx context.Context, token, deployment string, rep fly.Report) error
}

func buildToken(r *http.Request) string {
	tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Build ")
	return tok
}

func (s *Server) buildUpload(w http.ResponseWriter, r *http.Request) {
	var req fly.UploadRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	url, err := s.Builds.UploadURL(r.Context(), buildToken(r), r.PathValue("id"), req)
	if err != nil {
		s.buildError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

func (s *Server) buildReport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	var rep fly.Report
	if !decodeJSON(w, r, &rep) {
		return
	}
	if err := s.Builds.Add(r.Context(), buildToken(r), r.PathValue("id"), rep); err != nil {
		s.buildError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) buildError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, fly.ErrBadToken):
		writeError(w, http.StatusUnauthorized, "invalid_build_token", "This build token is invalid, expired, or its build is over.")
	case errors.Is(err, fly.ErrBadReport):
		writeError(w, http.StatusBadRequest, "invalid_report", err.Error())
	default:
		s.internalError(w, r, err)
	}
}
