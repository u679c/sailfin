package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"tracelite/internal/device"
	"tracelite/internal/protocol"
)

var browserTypes = map[string]bool{"page_view": true, "route_change": true, "click": true, "session_start": true, "page_hidden": true, "js_error": true, "unhandled_rejection": true, "performance_event": true}
var propertyTypes = map[string]bool{"element": true, "elementId": true, "label": true, "selector": true, "region": true, "pagePath": true, "status": true, "method": true, "bytes": true, "durationMs": true, "message": true, "metric": true, "value": true, "referrer": true}

func (s *Server) browserProject(r *http.Request, p string) (map[string]any, error) {
	rows, e := s.rows(r.Context(), "SELECT * FROM projects WHERE id=? AND status='active' AND browser_enabled=1", p)
	if e != nil {
		return nil, e
	}
	if len(rows) != 1 {
		return nil, fail(403, "browser collection is disabled")
	}
	var origins []string
	_ = json.Unmarshal([]byte(rows[0]["domains"].(string)), &origins)
	origin := r.Header.Get("Origin")
	for _, o := range origins {
		if o == origin && origin != "" {
			return rows[0], nil
		}
	}
	return nil, fail(403, "origin is not allowed for this project")
}
func (s *Server) cors(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
	w.Header().Set("Vary", "Origin")
	w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}
func (s *Server) browserOptions(w http.ResponseWriter, r *http.Request) error {
	p := r.URL.Query().Get("projectId")
	if _, e := s.browserProject(r, p); e != nil {
		return e
	}
	s.cors(w, r)
	w.WriteHeader(204)
	return nil
}
func (s *Server) browserConfig(w http.ResponseWriter, r *http.Request) error {
	p, e := s.browserProject(r, r.URL.Query().Get("projectId"))
	if e != nil {
		return e
	}
	s.cors(w, r)
	var c map[string]any
	_ = json.Unmarshal([]byte(p["collection"].(string)), &c)
	if c == nil {
		c = map[string]any{}
	}
	c["enabled"] = true
	jsonOut(w, 200, c)
	return nil
}
func (s *Server) browserEvents(w http.ResponseWriter, r *http.Request) error {
	p := r.URL.Query().Get("projectId")
	project, e := s.browserProject(r, p)
	if e != nil {
		return e
	}
	s.cors(w, r)
	var b struct {
		Events []protocol.Event `json:"events"`
	}
	if e = decode(r, &b); e != nil {
		return e
	}
	for i := range b.Events {
		if !browserTypes[b.Events[i].EventType] {
			return fail(400, "invalid browser event type")
		}
		b.Events[i].UA = r.UserAgent()
	}
	var c map[string]any
	_ = json.Unmarshal([]byte(project["collection"].(string)), &c)
	for _, ev := range b.Events {
		if list, ok := c["eventTypes"].([]any); ok {
			found := false
			for _, x := range list {
				if x == ev.EventType {
					found = true
				}
			}
			if !found {
				return fail(403, "event type excluded by project policy")
			}
		}
		if list, ok := c["excludePaths"].([]any); ok {
			for _, x := range list {
				if prefix, ok := x.(string); ok && strings.HasPrefix(ev.Path, prefix) {
					return fail(403, "path excluded by project policy")
				}
			}
		}
	}
	return s.ingest(w, r, p, "browser", "", b.Events)
}
func (s *Server) agentEvents(w http.ResponseWriter, r *http.Request) error {
	id, p, e := s.agentIdentity(r)
	if e != nil {
		return e
	}
	var b struct {
		Events []protocol.Event `json:"events"`
	}
	if e = decode(r, &b); e != nil {
		return e
	}
	for _, ev := range b.Events {
		if ev.EventType != "http_request" || ev.SessionID != "" || ev.VisitorID != "" {
			return fail(400, "host collectors accept anonymous HTTP requests only")
		}
	}
	return s.ingest(w, r, p, "host", id, b.Events)
}
func validateEvent(e protocol.Event, p string) error {
	raw, _ := json.Marshal(e)
	if len(raw) > 8192 || e.ProjectID != p || len(e.EventID) < 1 || len(e.EventID) > 128 || len(e.SessionID) > 128 || len(e.VisitorID) > 128 || len(e.UA) > 1000 || len(e.Path) > 2048 || !strings.HasPrefix(e.Path, "/") || strings.ContainsAny(e.Path, "?#\r\n") || len(e.Properties) > 12 {
		return fail(400, "event exceeds schema limits or has invalid project/path")
	}
	if e.Timestamp < Now()-int64(31*24*time.Hour/time.Millisecond) || e.Timestamp > Now()+300000 {
		return fail(400, "event timestamp is outside accepted range")
	}
	if e.EventType != "http_request" && (e.SessionID == "" || e.VisitorID == "") {
		return fail(400, "browser events require anonymous session and visitor identifiers")
	}
	for k, v := range e.Properties {
		if !propertyTypes[k] {
			return fail(400, "event property is not allowed: "+k)
		}
		switch n := v.(type) {
		case string:
			if len(n) > 500 || strings.ContainsAny(n, "\r\n") {
				return fail(400, "invalid event property")
			}
			if k == "referrer" {
				u, err := url.Parse(n)
				if err != nil || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
					return fail(400, "unsafe referrer")
				}
			}
		case float64:
			if n < 0 || n > 1e12 {
				return fail(400, "invalid numeric property")
			}
		default:
			return fail(400, "event properties must be flat strings or numbers")
		}
	}
	return nil
}
func (s *Server) ingest(w http.ResponseWriter, r *http.Request, p, source, agent string, events []protocol.Event) error {
	return s.ingestBatch(w, r, p, source, agent, events, "")
}

func (s *Server) ingestBatch(w http.ResponseWriter, r *http.Request, p, source, agent string, events []protocol.Event, siteName string) error {
	if len(events) < 1 || len(events) > 100 {
		return fail(400, "batch must contain 1..100 events")
	}
	for _, ev := range events {
		if e := validateEvent(ev, p); e != nil {
			s.count("events_rejected")
			return e
		}
	}
	tx, e := s.DB.BeginTx(r.Context(), nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if siteName != "" {
		// Recheck the host inside the write transaction so revocation wins over a
		// batch that has authenticated but has not started writing.
		var active int
		token := hash(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		local := s.cfg.LocalOnly && agent == LocalProbeID
		if e = tx.QueryRow("SELECT count(*) FROM agents WHERE id=? AND project_id='' AND enabled=1 AND (? OR token=?)", agent, local, token).Scan(&active); e != nil {
			return e
		}
		if active != 1 {
			if local {
				return fail(403, "local probe is paused")
			}
			return fail(401, "invalid or disabled host")
		}
		if local && strings.HasPrefix(p, "site_") {
			// Dynamic Host names from a default vhost must not create an
			// unbounded number of site records.
			var exists, total int
			if e = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM projects WHERE id=?), (SELECT count(*) FROM (SELECT 1 FROM projects WHERE id GLOB 'site_*' LIMIT 500))", p).Scan(&exists, &total); e != nil {
				return e
			}
			if exists == 0 && total >= 500 {
				return fail(429, "site capacity reached")
			}
		}
		if _, e = tx.Exec("INSERT INTO projects(id,name,description,created_at) VALUES(?,?,?,?) ON CONFLICT(id) DO NOTHING", p, siteName, "", Now()); e != nil {
			return e
		}
	}
	var quota, used int
	if e = tx.QueryRow("SELECT daily_quota FROM projects WHERE id=? AND status='active'", p).Scan(&quota); e != nil {
		return fail(403, "project unavailable")
	}
	day := time.Now().UTC().Format("2006-01-02")
	if _, e = tx.Exec("INSERT OR IGNORE INTO daily_usage VALUES(?,?,0)", p, day); e != nil {
		return e
	}
	if e = tx.QueryRow("SELECT count FROM daily_usage WHERE project_id=? AND day=?", p, day).Scan(&used); e != nil {
		return e
	}
	accepted := 0
	for _, ev := range events {
		var exists int
		if e = tx.QueryRow("SELECT count(*) FROM events WHERE project_id=? AND id=?", p, ev.EventID).Scan(&exists); e != nil {
			return e
		}
		if exists > 0 {
			continue
		}
		if used+accepted >= quota {
			_ = tx.Rollback()
			s.count("quota_rejected")
			return fail(429, "project daily write quota exceeded")
		}
		d := device.Parse(ev.UA)
		info, _ := json.Marshal(d)
		props, _ := json.Marshal(ev.Properties)
		_, e = tx.Exec("INSERT INTO events VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)", ev.EventID, p, ev.SessionID, ev.VisitorID, ev.EventType, ev.Timestamp, Now(), ev.Path, string(props), source, agent, d.Type, d.Browser, d.OS, string(info))
		if e != nil {
			return e
		}
		accepted++
	}
	if _, e = tx.Exec("UPDATE daily_usage SET count=count+? WHERE project_id=? AND day=?", accepted, p, day); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	jsonOut(w, 200, map[string]int{"accepted": accepted, "duplicates": len(events) - accepted})
	return nil
}
func bounds(r *http.Request) (int64, int64, error) {
	end := Now()
	start := end - 86400000
	var e error
	if x := r.URL.Query().Get("from"); x != "" {
		start, e = strconv.ParseInt(x, 10, 64)
		if e != nil {
			return 0, 0, fail(400, "invalid from")
		}
	}
	if x := r.URL.Query().Get("to"); x != "" {
		end, e = strconv.ParseInt(x, 10, 64)
		if e != nil {
			return 0, 0, fail(400, "invalid to")
		}
	}
	if start < 0 || end < start || end-start > int64(366*24*time.Hour/time.Millisecond) {
		return 0, 0, fail(400, "time range must be within 366 days")
	}
	return start, end, nil
}
func filter(r *http.Request) (string, []any, error) {
	from, to, e := bounds(r)
	if e != nil {
		return "", nil, e
	}
	q := " timestamp>=? AND timestamp<=?"
	args := []any{from, to}
	for _, v := range []struct{ key, col string }{{"projectId", "project_id"}, {"agentId", "agent_id"}, {"eventType", "type"}} {
		if x := r.URL.Query().Get(v.key); x != "" {
			q += " AND " + v.col + "=?"
			args = append(args, x)
		}
	}
	return q, args, nil
}
func pagination(r *http.Request) (int, int) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	if page > 10000 {
		page = 10000
	}
	return limit, (page - 1) * limit
}
func (s *Server) events(w http.ResponseWriter, r *http.Request) error {
	q, a, e := filter(r)
	if e != nil {
		return e
	}
	limit, offset := pagination(r)
	a = append(a, limit, offset)
	return s.query(w, r, "SELECT * FROM events WHERE "+q+" ORDER BY timestamp DESC,id LIMIT ? OFFSET ?", a...)
}
func (s *Server) sessions(w http.ResponseWriter, r *http.Request) error {
	q, a, e := filter(r)
	if e != nil {
		return e
	}
	limit, offset := pagination(r)
	a = append(a, limit, offset)
	return s.query(w, r, `SELECT project_id,session_id,min(timestamp) AS started_at,max(timestamp) AS last_activity,max(device) AS device,max(browser) AS browser,max(os) AS os,count(CASE WHEN type IN ('page_view','route_change') THEN 1 END) AS pages,count(CASE WHEN type='click' THEN 1 END) AS interactions,COALESCE(sum(CASE WHEN type='page_hidden' THEN json_extract(properties,'$.durationMs') ELSE 0 END),0) AS duration_ms FROM events WHERE session_id!='' AND `+q+` GROUP BY project_id,session_id ORDER BY last_activity DESC LIMIT ? OFFSET ?`, a...)
}
func (s *Server) sessionDetail(w http.ResponseWriter, r *http.Request) error {
	p := r.URL.Query().Get("projectId")
	if p == "" {
		return fail(400, "projectId is required for session isolation")
	}
	limit, offset := pagination(r)
	return s.query(w, r, "SELECT * FROM events WHERE project_id=? AND session_id=? ORDER BY timestamp,id LIMIT ? OFFSET ?", p, r.PathValue("id"), limit, offset)
}
func (s *Server) stats(w http.ResponseWriter, r *http.Request, kind string) error {
	q, a, e := filter(r)
	if e != nil {
		return e
	}
	switch kind {
	case "overview":
		return s.query(w, r, `SELECT count(*) AS events,count(CASE WHEN type='http_request' THEN 1 END) AS requests,count(CASE WHEN type IN ('page_view','route_change') THEN 1 END) AS pv,count(DISTINCT CASE WHEN visitor_id!='' THEN project_id||':'||visitor_id END) AS uv,count(DISTINCT CASE WHEN session_id!='' AND timestamp>`+strconv.FormatInt(Now()-1800000, 10)+` THEN project_id||':'||session_id END) AS active_sessions,count(CASE WHEN type IN ('js_error','unhandled_rejection') THEN 1 END) AS js_errors,COALESCE(avg(CASE WHEN type='page_hidden' THEN json_extract(properties,'$.durationMs') END),0) AS avg_duration_ms FROM events WHERE `+q, a...)
	case "trends":
		return s.query(w, r, `SELECT strftime('%Y-%m-%d %H:00',timestamp/1000,'unixepoch') AS time,count(*) AS events,count(CASE WHEN type='http_request' THEN 1 END) AS requests,count(CASE WHEN type IN ('page_view','route_change') THEN 1 END) AS pv,count(DISTINCT CASE WHEN visitor_id!='' THEN project_id||':'||visitor_id END) AS uv,count(CASE WHEN type='http_request' AND json_extract(properties,'$.status')>=400 THEN 1 END) AS errors FROM events WHERE `+q+` GROUP BY time ORDER BY time LIMIT 9000`, a...)
	case "devices":
		return s.query(w, r, `SELECT device,browser,os,count(*) AS events,count(CASE WHEN type='http_request' THEN 1 END) AS requests,count(CASE WHEN type IN ('page_view','route_change') THEN 1 END) AS pv,count(DISTINCT CASE WHEN visitor_id!='' THEN project_id||':'||visitor_id END) AS uv FROM events WHERE `+q+` GROUP BY device,browser,os ORDER BY events DESC LIMIT 100`, a...)
	default:
		return s.query(w, r, `SELECT path,count(*) AS events,count(CASE WHEN type='http_request' THEN 1 END) AS requests,count(CASE WHEN type IN ('page_view','route_change') THEN 1 END) AS pv FROM events WHERE `+q+` GROUP BY path ORDER BY events DESC LIMIT 100`, a...)
	}
}
func (s *Server) security(w http.ResponseWriter, r *http.Request) error {
	c, e := s.rows(r.Context(), "SELECT * FROM security_counters ORDER BY name")
	if e != nil {
		return e
	}
	a, e := s.rows(r.Context(), "SELECT * FROM audit_logs ORDER BY id DESC LIMIT 100")
	if e != nil {
		return e
	}
	u, e := s.rows(r.Context(), "SELECT p.name,p.daily_quota,COALESCE(d.count,0) AS used FROM projects p LEFT JOIN daily_usage d ON d.project_id=p.id AND d.day=?", time.Now().UTC().Format("2006-01-02"))
	if e != nil {
		return e
	}
	alerts, e := s.rows(r.Context(), "SELECT id,name,enabled,last_seen,error,metrics FROM agents WHERE (?=0 OR id=?) AND enabled=1 AND (error!='' OR last_seen<?)", s.cfg.LocalOnly, LocalProbeID, Now()-int64(s.cfg.OfflineSeconds)*1000)
	if e != nil {
		return e
	}
	jsonOut(w, 200, map[string]any{"counters": c, "audit": a, "quotas": u, "agents": alerts, "diskFreeBytes": diskFree(filepath.Dir(s.cfg.DBPath))})
	return nil
}
func (s *Server) system(w http.ResponseWriter, r *http.Request) error {
	var size int64
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if f, e := os.Stat(s.cfg.DBPath + suffix); e == nil {
			size += f.Size()
		}
	}
	jsonOut(w, 200, map[string]any{"version": "0.1.0-dev", "databaseBytes": size, "diskFreeBytes": diskFree(filepath.Dir(s.cfg.DBPath)), "offlineSeconds": s.cfg.OfflineSeconds, "publicUrl": s.cfg.PublicURL, "database": "SQLite WAL", "dockerMode": "authorized host collector (read only)", "timezone": "UTC"})
	return nil
}
func (s *Server) backup(w http.ResponseWriter, r *http.Request) error {
	dir := filepath.Join(filepath.Dir(s.cfg.DBPath), "backups")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	name := "tracelite-" + time.Now().UTC().Format("20060102T150405") + "-" + Random()[:6] + ".db"
	path := filepath.Join(dir, name)
	defer os.Remove(path)
	if _, e := s.DB.ExecContext(r.Context(), "VACUUM INTO ?", path); e != nil {
		return e
	}
	if e := os.Chmod(path, 0600); e != nil {
		return e
	}
	s.audit("database.backup", name)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	http.ServeFile(w, r, path)
	return nil
}
func (s *Server) Maintain(ctxDone <-chan struct{}) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctxDone:
			return
		case <-t.C:
			s.cleanup()
		}
	}
}
func (s *Server) cleanup() {
	tx, e := s.DB.Begin()
	if e != nil {
		return
	}
	defer tx.Rollback()
	queries := []string{`DELETE FROM events WHERE EXISTS(SELECT 1 FROM projects p WHERE p.id=events.project_id AND events.timestamp < ? - p.retention_days*86400000)`, `DELETE FROM auth_sessions WHERE expires<?`, `DELETE FROM enrollments WHERE expires<?`, `DELETE FROM daily_usage WHERE day<date('now','-366 days')`, `DELETE FROM agent_logs WHERE id NOT IN (SELECT id FROM agent_logs ORDER BY id DESC LIMIT 2000)`, `DELETE FROM audit_logs WHERE id NOT IN (SELECT id FROM audit_logs ORDER BY id DESC LIMIT 10000)`, `DELETE FROM agent_configs WHERE version < (SELECT max(a.version)-19 FROM agent_configs a WHERE a.agent_id=agent_configs.agent_id)`,
		`DELETE FROM project_configs WHERE version < (SELECT max(p.version)-19 FROM project_configs p WHERE p.project_id=project_configs.project_id)`}
	for i, q := range queries {
		var err error
		if i < 3 {
			_, err = tx.Exec(q, Now())
		} else {
			_, err = tx.Exec(q)
		}
		if err != nil {
			return
		}
	}
	_ = tx.Commit()
}
