package api

import (
	"context"
	"errors"
	"net/http"

	"greyship.tech/internal/mail"
)

// MailSettings is the instance's email setup, edited by an admin in the dashboard (decisions.md 052).
type MailSettings interface {
	Status(ctx context.Context) mail.Status
	Save(ctx context.Context, in mail.Settings) error
	SendTest(ctx context.Context, to string) error
}

func (s *Server) adminMailGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Mail.Status(r.Context()))
}

func (s *Server) adminMailPut(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled       bool   `json:"enabled"`
		SendingDomain string `json:"sending_domain"`
		FromName      string `json:"from_name"`
		APIKey        string `json:"api_key"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	err := s.Mail.Save(r.Context(), mail.Settings{Enabled: body.Enabled, SendingDomain: body.SendingDomain, FromName: body.FromName, APIKey: body.APIKey})
	switch {
	case errors.Is(err, mail.ErrInvalidDomain):
		writeError(w, http.StatusBadRequest, "invalid_domain", err.Error()+".")
	case errors.Is(err, mail.ErrInvalidKey):
		writeError(w, http.StatusBadRequest, "invalid_key", "That doesn't look like a Resend API key. It starts with re_.")
	case errors.Is(err, mail.ErrInvalidName):
		writeError(w, http.StatusBadRequest, "invalid_name", "The sender name can't be empty, longer than 64 characters or contain < > \" or line breaks.")
	case errors.Is(err, mail.ErrNeedKey), errors.Is(err, mail.ErrNeedDomain):
		writeError(w, http.StatusBadRequest, "incomplete", "Add the Resend API key and the sending domain before turning email on.")
	case err != nil:
		s.internalError(w, r, err)
	default:
		s.Logger.Info("admin changed email settings", "by", userFrom(r.Context()).ID, "enabled", body.Enabled, "key_changed", body.APIKey != "")
		writeJSON(w, http.StatusOK, s.Mail.Status(r.Context()))
	}
}

func (s *Server) adminMailTest(w http.ResponseWriter, r *http.Request) {
	if err := s.Mail.SendTest(r.Context(), userFrom(r.Context()).Email); err != nil {
		writeError(w, http.StatusBadGateway, "send_failed", "The test email failed: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
