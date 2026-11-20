package core

import (
	"context"
	"net/http"
)

type NginxSiteResult struct {
	Site   string `json:"site"`
	File   string `json:"file,omitempty"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type NginxPreview struct {
	ID    string            `json:"id"`
	Ready int               `json:"ready"`
	Sites []NginxSiteResult `json:"sites"`
}

type NginxIntegration interface {
	Scan(context.Context) (NginxPreview, error)
	Apply(context.Context, string) (NginxPreview, error)
}

func (s *Server) scanNginx(w http.ResponseWriter, r *http.Request) error {
	result, err := s.cfg.Nginx.Scan(r.Context())
	if err != nil {
		return fail(409, err.Error())
	}
	jsonOut(w, http.StatusOK, result)
	return nil
}

func (s *Server) applyNginx(w http.ResponseWriter, r *http.Request) error {
	var request struct {
		ID string `json:"id"`
	}
	if err := decode(r, &request); err != nil {
		return err
	}
	result, err := s.cfg.Nginx.Apply(r.Context(), request.ID)
	if err != nil {
		return fail(409, err.Error())
	}
	s.audit("nginx.enable", "")
	jsonOut(w, http.StatusOK, result)
	return nil
}
