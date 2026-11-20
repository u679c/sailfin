package core

import (
	"net/http"
	"strings"

	"tracelite/internal/protocol"
)

func (s *Server) collectedSites(w http.ResponseWriter, r *http.Request) error {
	return s.query(w, r, `SELECT p.id,p.name,p.created_at,
		(SELECT max(e.received_at) FROM events e WHERE e.project_id=p.id) AS last_event
		FROM projects p
		WHERE p.id GLOB 'site_*' OR EXISTS(SELECT 1 FROM events e WHERE e.project_id=p.id)
		ORDER BY last_event DESC,p.created_at DESC LIMIT 500`)
}

func (s *Server) siteEvents(w http.ResponseWriter, r *http.Request) error {
	agentID, project, err := s.agentIdentity(r)
	if err != nil {
		return err
	}
	if project != "" {
		return fail(403, "site collection requires a host enrollment")
	}
	return s.ingestSiteEvents(w, r, agentID)
}

func (s *Server) ingestSiteEvents(w http.ResponseWriter, r *http.Request, agentID string) error {
	var batch protocol.SiteBatch
	if err := decode(r, &batch); err != nil {
		return err
	}
	name := strings.TrimSpace(batch.Name)
	if !protocol.ValidSiteID(batch.SiteID) || name == "" || len(name) > 120 {
		return fail(400, "invalid site identity")
	}
	if batch.Source != "browser" && batch.Source != "host" {
		return fail(400, "unknown collection source")
	}

	// The stable operator-assigned ID merges the two ingestion paths and replicas.
	projectID := "site_" + hash(batch.SiteID)[:32]
	for i := range batch.Events {
		event := &batch.Events[i]
		if batch.Source == "host" {
			if event.EventType != "http_request" || event.SessionID != "" || event.VisitorID != "" {
				return fail(400, "invalid host event")
			}
		} else if !browserTypes[event.EventType] {
			return fail(400, "invalid browser event")
		}
		event.ProjectID = projectID
	}
	return s.ingestBatch(w, r, projectID, batch.Source, agentID, batch.Events, name)
}
