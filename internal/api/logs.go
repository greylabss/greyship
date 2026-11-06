package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

type appLogJSON struct {
	ID       int64  `json:"id"`
	Time     string `json:"ts"`
	Service  string `json:"service"`
	Instance string `json:"instance"`
	Stream   string `json:"stream"`
	Line     string `json:"line"`
}

// projectLogs streams what a project's apps print (F-60) as newline-delimited JSON. It starts with the last
// tail lines (default 100), then with follow=1 keeps sending new ones, with {"ping":true} while quiet.
func (s *Server) projectLogs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c := callerFrom(ctx)
	query := r.URL.Query()
	service, follow := query.Get("service"), query.Get("follow") == "1"
	tail := 100
	if v := query.Get("tail"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 1000 {
			writeError(w, http.StatusBadRequest, "invalid_tail", "tail must be a number from 0 to 1000.")
			return
		}
		tail = n
	}
	var after int64
	rows, err := s.Deployments.AppLogs(ctx, c, r.PathValue("id"), service, 0, int32(max(tail, 1))) //nolint:gosec // bounded above
	if err != nil {
		s.deploymentError(w, r, err)
		return
	}
	if tail == 0 {
		// Only the newest line's position is needed: follow from there.
		if len(rows) > 0 {
			after = rows[len(rows)-1].ID
		}
		rows = nil
	}
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	lastWrite := time.Now()
	for {
		for _, l := range rows {
			_ = enc.Encode(appLogJSON{ID: l.ID, Time: l.Ts.UTC().Format(time.RFC3339Nano), Service: l.ServiceName, Instance: l.InstanceID[:8], Stream: l.Stream, Line: l.Line})
			after = l.ID
			lastWrite = time.Now()
		}
		if !follow {
			return
		}
		if time.Since(lastWrite) >= logPing {
			_ = enc.Encode(map[string]bool{"ping": true})
			lastWrite = time.Now()
		}
		if err := rc.Flush(); err != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(logPoll):
		}
		if rows, err = s.Deployments.AppLogs(ctx, c, r.PathValue("id"), service, after, 0); err != nil {
			if ctx.Err() == nil {
				s.Logger.Error("stream app logs", "project", r.PathValue("id"), "err", err)
			}
			return
		}
	}
}
