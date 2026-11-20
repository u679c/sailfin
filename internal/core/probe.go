package core

import (
	"log/slog"
	"net/http"

	"tracelite/internal/protocol"
)

const LocalProbeID = "local-probe"

func (s *Server) initProbe() error {
	_, err := s.DB.Exec(`INSERT INTO agents(id,project_id,name,token,created_at)
		VALUES(?,'','本机探针',?,?)
		ON CONFLICT(id) DO UPDATE SET last_seen=0,error=''`,
		LocalProbeID, hash(Random()), Now())
	return err
}

// ProbeHandler is handed directly to the embedded probe. It is never mounted
// on the public HTTP server.
func (s *Server) ProbeHandler() http.Handler {
	if !s.cfg.LocalOnly {
		return http.NotFoundHandler()
	}
	mux := http.NewServeMux()
	handle := func(pattern string, fn func(http.ResponseWriter, *http.Request) error) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
			}
			if err := fn(w, r); err != nil {
				s.reject(w, r, err)
			}
		})
	}
	handle("GET /api/v1/agent/config", func(w http.ResponseWriter, r *http.Request) error {
		var enabled bool
		err := s.DB.QueryRowContext(r.Context(), "SELECT enabled FROM agents WHERE id=?", LocalProbeID).Scan(&enabled)
		if err != nil {
			return err
		}
		jsonOut(w, 200, protocol.Config{Version: 1, Enabled: enabled, BatchSize: 50, IntervalSeconds: 5})
		return nil
	})
	handle("POST /api/v1/agent/heartbeat", func(w http.ResponseWriter, r *http.Request) error {
		return s.recordHeartbeat(w, r, LocalProbeID)
	})
	handle("POST /api/v1/agent/sites/batch", func(w http.ResponseWriter, r *http.Request) error {
		return s.ingestSiteEvents(w, r, LocalProbeID)
	})
	return mux
}

func (s *Server) ReportProbeFailure(err error) {
	if _, writeErr := s.DB.Exec("UPDATE agents SET last_seen=0,error=? WHERE id=?", err.Error(), LocalProbeID); writeErr != nil {
		slog.Error("Could not record local probe status", "error", writeErr)
	}
}

func (s *Server) probeStatus(w http.ResponseWriter, r *http.Request) error {
	rows, err := s.rows(r.Context(), `SELECT enabled, hostname, os, arch, version, last_seen, metrics, error,
		CASE WHEN enabled=0 THEN 'Disabled'
		     WHEN last_seen=0 AND error!='' THEN 'Failed'
		     WHEN last_seen=0 THEN 'Starting'
		     WHEN last_seen<? THEN 'Offline'
		     WHEN error!='' THEN 'Warning'
		     ELSE 'Online' END AS status
		FROM agents WHERE id=?`, Now()-int64(s.cfg.OfflineSeconds)*1000, LocalProbeID)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return fail(404, "local probe is unavailable")
	}
	jsonOut(w, 200, rows[0])
	return nil
}

func (s *Server) setProbeEnabled(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	if body.Enabled == nil {
		return fail(400, "enabled is required")
	}
	if _, err := s.DB.ExecContext(r.Context(), "UPDATE agents SET enabled=? WHERE id=?", *body.Enabled, LocalProbeID); err != nil {
		return err
	}
	s.audit("probe.enabled", LocalProbeID)
	jsonOut(w, 200, map[string]bool{"enabled": *body.Enabled})
	return nil
}
