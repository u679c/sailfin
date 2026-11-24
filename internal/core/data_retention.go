package core

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
)

type dataRetention struct {
	RetentionDays *int `json:"retentionDays"`
	MaxEventMiB   int  `json:"maxEventMiB"`
}

const bytesPerMiB = 1024 * 1024

// Count stored text in UTF-8 bytes and account for the two timestamps. SQLite
// pages, indexes and WAL have different lifecycles from the event rows.
const eventRowBytes = `16 + length(CAST(id AS BLOB)) + length(CAST(project_id AS BLOB))
	+ length(CAST(session_id AS BLOB)) + length(CAST(visitor_id AS BLOB)) + length(CAST(type AS BLOB))
	+ length(CAST(path AS BLOB)) + length(CAST(properties AS BLOB)) + length(CAST(source_type AS BLOB))
	+ length(CAST(agent_id AS BLOB)) + length(CAST(device AS BLOB)) + length(CAST(browser AS BLOB))
	+ length(CAST(os AS BLOB)) + length(CAST(device_info AS BLOB))`

func (s *Server) getDataRetention(w http.ResponseWriter, r *http.Request) error {
	var policy dataRetention
	if err := readJSONSetting(r.Context(), s.DB, "data.retention", &policy); err != nil {
		return err
	}
	var events, eventBytes int64
	if err := s.DB.QueryRowContext(r.Context(), "SELECT count(*),COALESCE(sum("+eventRowBytes+"),0) FROM events").Scan(&events, &eventBytes); err != nil {
		return err
	}
	jsonOut(w, 200, struct {
		dataRetention
		Events     int64 `json:"events"`
		EventBytes int64 `json:"eventBytes"`
	}{policy, events, eventBytes})
	return nil
}

func (s *Server) setDataRetention(w http.ResponseWriter, r *http.Request) error {
	var policy dataRetention
	if err := decode(r, &policy); err != nil {
		return err
	}
	if policy.RetentionDays != nil && (*policy.RetentionDays < 0 || *policy.RetentionDays > 3650) {
		return fail(400, "保留天数须为 0–3650")
	}
	if policy.MaxEventMiB < 0 || policy.MaxEventMiB > 1048576 {
		return fail(400, "容量上限须为 0–1048576 MiB")
	}
	if err := s.writeJSONSetting(r.Context(), "data.retention", policy); err != nil {
		return err
	}
	age := "site-default"
	if policy.RetentionDays != nil {
		age = fmt.Sprint(*policy.RetentionDays)
	}
	s.audit("database.retention", fmt.Sprintf("days=%s maxMiB=%d", age, policy.MaxEventMiB))
	jsonOut(w, 200, policy)
	return nil
}

func trimEvents(ctx context.Context, tx *sql.Tx, now int64) error {
	var policy dataRetention
	if err := readJSONSetting(ctx, tx, "data.retention", &policy); err != nil {
		return err
	}
	switch {
	case policy.RetentionDays == nil:
		if _, err := tx.ExecContext(ctx, `DELETE FROM events WHERE EXISTS(SELECT 1 FROM projects p
			WHERE p.id=events.project_id AND events.timestamp < ? - p.retention_days*86400000)`, now); err != nil {
			return err
		}
	case *policy.RetentionDays > 0:
		if _, err := tx.ExecContext(ctx, "DELETE FROM events WHERE timestamp < ?", now-int64(*policy.RetentionDays)*86400000); err != nil {
			return err
		}
	}
	if policy.MaxEventMiB == 0 {
		return nil
	}
	limit := int64(policy.MaxEventMiB) * bytesPerMiB
	var total int64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(sum("+eventRowBytes+"),0) FROM events").Scan(&total); err != nil {
		return err
	}
	if total <= limit {
		return nil
	}
	_, err := tx.ExecContext(ctx, `WITH newest AS (
		SELECT project_id,id,sum(`+eventRowBytes+`) OVER (
			ORDER BY timestamp DESC,project_id DESC,id DESC ROWS UNBOUNDED PRECEDING
		) AS retained_bytes FROM events
	) DELETE FROM events WHERE (project_id,id) IN (
		SELECT project_id,id FROM newest WHERE retained_bytes>?
	)`, limit)
	return err
}

func (s *Server) cleanup(ctx context.Context) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := Now()
	if err := trimEvents(ctx, tx, now); err != nil {
		return err
	}
	for _, query := range []string{"DELETE FROM auth_sessions WHERE expires<?", "DELETE FROM enrollments WHERE expires<?"} {
		if _, err := tx.ExecContext(ctx, query, now); err != nil {
			return err
		}
	}
	for _, query := range []string{
		`DELETE FROM daily_usage WHERE day<date('now','-366 days')`,
		`DELETE FROM agent_logs WHERE id NOT IN (SELECT id FROM agent_logs ORDER BY id DESC LIMIT 2000)`,
		`DELETE FROM audit_logs WHERE id NOT IN (SELECT id FROM audit_logs ORDER BY id DESC LIMIT 10000)`,
		`DELETE FROM agent_configs WHERE version < (SELECT max(a.version)-19 FROM agent_configs a WHERE a.agent_id=agent_configs.agent_id)`,
		`DELETE FROM project_configs WHERE version < (SELECT max(p.version)-19 FROM project_configs p WHERE p.project_id=project_configs.project_id)`,
	} {
		if _, err := tx.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	return tx.Commit()
}
