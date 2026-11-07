package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"greyship.tech/internal/domains"
	"greyship.tech/internal/project"
)

// Domains points custom hostnames at a project's web services (spec F-51, decisions.md 057).
type Domains interface {
	List(ctx context.Context, c project.Caller, projectID string) ([]domains.Entry, error)
	Add(ctx context.Context, c project.Caller, projectID, hostname, service string) (domains.Entry, error)
	Remove(ctx context.Context, c project.Caller, projectID, hostname string) error
}

type domainJSON struct {
	Hostname string `json:"hostname"`
	Service  string `json:"service"`
	Custom   bool   `json:"custom"`
	// DNS is pointing, not_pointing, unresolved or unknown, for custom hostnames; empty for the default address.
	DNS string `json:"dns,omitempty"`
}

func toDomainJSON(e domains.Entry) domainJSON {
	return domainJSON{Hostname: e.Hostname, Service: e.Service, Custom: e.Custom, DNS: e.DNS}
}

func (s *Server) listDomains(w http.ResponseWriter, r *http.Request) {
	entries, err := s.Domains.List(r.Context(), callerFrom(r.Context()), r.PathValue("id"))
	if err != nil {
		s.domainError(w, r, err)
		return
	}
	out := make([]domainJSON, 0, len(entries))
	for _, e := range entries {
		out = append(out, toDomainJSON(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{"domains": out})
}

func (s *Server) addDomain(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Hostname string `json:"hostname"`
		Service  string `json:"service"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	e, err := s.Domains.Add(r.Context(), callerFrom(r.Context()), r.PathValue("id"), body.Hostname, body.Service)
	if err != nil {
		s.domainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]domainJSON{"domain": toDomainJSON(e)})
}

func (s *Server) removeDomain(w http.ResponseWriter, r *http.Request) {
	host, err := url.PathUnescape(r.PathValue("hostname"))
	if err == nil {
		err = s.Domains.Remove(r.Context(), callerFrom(r.Context()), r.PathValue("id"), host)
	}
	if err != nil {
		s.domainError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) domainError(w http.ResponseWriter, r *http.Request, err error) {
	var invalid *domains.InvalidError
	switch {
	case errors.As(err, &invalid):
		writeError(w, http.StatusBadRequest, "invalid_hostname", "That hostname can't be used: "+invalid.Reason+".")
	case errors.Is(err, domains.ErrReserved):
		writeError(w, http.StatusBadRequest, "reserved_hostname", err.Error()+".")
	case errors.Is(err, domains.ErrTaken):
		writeError(w, http.StatusConflict, "hostname_taken", err.Error()+".")
	case errors.Is(err, domains.ErrNoWebService):
		writeError(w, http.StatusConflict, "no_web_service", err.Error()+".")
	case errors.Is(err, domains.ErrServiceRequired):
		writeError(w, http.StatusBadRequest, "service_required", err.Error()+".")
	case errors.Is(err, domains.ErrUnknownService):
		writeError(w, http.StatusBadRequest, "unknown_service", err.Error()+".")
	case errors.Is(err, domains.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error()+".")
	default:
		s.projectError(w, r, err)
	}
}
