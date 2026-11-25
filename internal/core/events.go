package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sailfin/internal/branding"
	"sailfin/internal/device"
	"sailfin/internal/protocol"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

var browserTypes = map[string]bool{"page_view": true, "route_change": true, "click": true, "session_start": true, "page_hidden": true, "js_error": true, "unhandled_rejection": true, "performance_event": true}
var propertyTypes = map[string]bool{"element": true, "elementId": true, "label": true, "selector": true, "ruleSelector": true, "region": true, "pagePath": true, "pageTitle": true, "status": true, "method": true, "bytes": true, "durationMs": true, "message": true, "metric": true, "value": true, "referrer": true}
var domTag = regexp.MustCompile(`^[a-z][a-z0-9_.:-]{0,63}$`)

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
	delete(c, "clickDescriptions")
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
		if k == "domPath" {
			path, ok := v.([]any)
			if e.EventType != "click" || !ok || len(path) == 0 || len(path) > 32 {
				return fail(400, "domPath must contain 1..32 element tags on a click event")
			}
			for _, node := range path {
				tag, ok := node.(string)
				if !ok || !domTag.MatchString(tag) {
					return fail(400, "invalid domPath element tag")
				}
			}
			continue
		}
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
	var enabledSince int64
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
		if local {
			e = tx.QueryRowContext(r.Context(), "SELECT enabled_since FROM site_registry WHERE project_id=? AND enabled=1", p).Scan(&enabledSince)
			if errors.Is(e, sql.ErrNoRows) {
				jsonOut(w, 200, map[string]int{"accepted": 0, "duplicates": 0})
				return nil
			}
			if e != nil {
				return e
			}
		}
		// Only the entry HTML title names the site; inner-page and hash-router
		// titles must not rename it as visitors move around the application.
		title := ""
		if source == "browser" {
			for _, ev := range events {
				if ev.Timestamp < enabledSince || (ev.Path != "/" && ev.Path != "/index.html" && ev.Path != "/index.htm") {
					continue
				}
				pagePath, _ := ev.Properties["pagePath"].(string)
				pageTitle, _ := ev.Properties["pageTitle"].(string)
				if strings.Contains(pagePath, "#") || strings.TrimSpace(pageTitle) == "" {
					continue
				}
				title = strings.Join(strings.Fields(pageTitle), " ")
				// Site names have a 120-byte limit. Truncate at a rune boundary.
				runes := []rune(title)
				for len(string(runes)) > 120 {
					runes = runes[:len(runes)-1]
				}
				title = string(runes)
			}
		}
		if title != "" {
			siteName = title
		}
		if _, e = tx.Exec("INSERT INTO projects(id,name,description,created_at) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=CASE WHEN ?<>'' THEN ? ELSE projects.name END", p, siteName, "", Now(), title, title); e != nil {
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
	discarded := 0
	for _, ev := range events {
		if ev.Timestamp < enabledSince {
			discarded++
			continue
		}
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
	jsonOut(w, 200, map[string]int{"accepted": accepted, "duplicates": len(events) - accepted - discarded})
	return nil
}
func (s *Server) bounds(r *http.Request) (int64, int64, error) {
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
	if fromLocal, toLocal := r.URL.Query().Get("fromLocal"), r.URL.Query().Get("toLocal"); fromLocal != "" || toLocal != "" {
		name, err := s.systemTimezone(r.Context())
		if err != nil {
			return 0, 0, err
		}
		loc, err := time.LoadLocation(name)
		if err != nil {
			return 0, 0, err
		}
		if toLocal != "" {
			end, err = parseLocalTime(toLocal, loc)
			if err != nil {
				return 0, 0, err
			}
			if r.URL.Query().Get("from") == "" {
				start = end - 86400000
			}
		}
		if fromLocal != "" {
			start, err = parseLocalTime(fromLocal, loc)
			if err != nil {
				return 0, 0, err
			}
		}
	}
	if start < 0 || end < start || end-start > int64(366*24*time.Hour/time.Millisecond) {
		return 0, 0, fail(400, "time range must be within 366 days")
	}
	return start, end, nil
}

func parseLocalTime(value string, loc *time.Location) (int64, error) {
	const layout = "2006-01-02T15:04"
	t, err := time.ParseInLocation(layout, value, loc)
	if err != nil || t.Format(layout) != value {
		return 0, fail(400, "所选时区下的日期或时间无效")
	}
	return t.UnixMilli(), nil
}

func (s *Server) filter(r *http.Request) (string, []any, error) {
	from, to, e := s.bounds(r)
	if e != nil {
		return "", nil, e
	}
	q := " timestamp>=? AND timestamp<=?"
	args := []any{from, to}
	if s.cfg.LocalOnly && r.URL.Query().Get("projectId") == "" {
		q += " AND project_id IN (SELECT project_id FROM site_registry WHERE enabled=1)"
	}
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
		limit = 20
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	// Bound arithmetic without imposing an artificial last page on large lists.
	if maxPage := int(^uint(0)>>1) / limit; page > maxPage {
		page = maxPage
	}
	return limit, (page - 1) * limit
}

// pagedQuery keeps the array response and reports whether another page exists.
func (s *Server) pagedQuery(w http.ResponseWriter, r *http.Request, query string, args ...any) error {
	limit, offset := pagination(r)
	var total int
	countQuery := "SELECT count(*) FROM (" + strings.TrimSuffix(strings.TrimSpace(query), "LIMIT ? OFFSET ?") + ")"
	if err := s.DB.QueryRowContext(r.Context(), countQuery, args...).Scan(&total); err != nil {
		return err
	}
	rows, err := s.rows(r.Context(), query, append(args, limit+1, offset)...)
	if err != nil {
		return err
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	w.Header().Set("X-Has-More", strconv.FormatBool(more))
	w.Header().Set("X-Total-Count", strconv.Itoa(total))
	jsonOut(w, 200, rows)
	return nil
}
func (s *Server) events(w http.ResponseWriter, r *http.Request) error {
	q, a, e := s.filter(r)
	if e != nil {
		return e
	}
	return s.pagedQuery(w, r, `SELECT events.*,
		CASE WHEN type='click' AND source_type='browser' THEN (
			SELECT d.value FROM projects p CROSS JOIN json_each(p.collection,'$.clickDescriptions') d
			WHERE p.id=events.project_id AND d.key=NULLIF(json_extract(events.properties,'$.ruleSelector'),'')
		) END AS click_description
		FROM events WHERE `+q+` ORDER BY timestamp DESC,id LIMIT ? OFFSET ?`, a...)
}
func (s *Server) sessions(w http.ResponseWriter, r *http.Request) error {
	q, a, e := s.filter(r)
	if e != nil {
		return e
	}
	return s.pagedQuery(w, r, `SELECT project_id,session_id,min(timestamp) AS started_at,max(timestamp) AS last_activity,max(device) AS device,max(browser) AS browser,max(os) AS os,count(CASE WHEN type IN ('page_view','route_change') THEN 1 END) AS pages,count(CASE WHEN type='click' THEN 1 END) AS interactions,COALESCE(sum(CASE WHEN type='page_hidden' THEN json_extract(properties,'$.durationMs') ELSE 0 END),0) AS duration_ms FROM events WHERE session_id!='' AND `+q+` GROUP BY project_id,session_id ORDER BY last_activity DESC,project_id,session_id LIMIT ? OFFSET ?`, a...)
}
func (s *Server) sessionDetail(w http.ResponseWriter, r *http.Request) error {
	p := r.URL.Query().Get("projectId")
	if p == "" {
		return fail(400, "projectId is required for session isolation")
	}
	return s.pagedQuery(w, r, "SELECT * FROM events WHERE project_id=? AND session_id=? ORDER BY timestamp,id LIMIT ? OFFSET ?", p, r.PathValue("id"))
}
func (s *Server) stats(w http.ResponseWriter, r *http.Request, kind string) error {
	q, a, e := s.filter(r)
	if e != nil {
		return e
	}
	switch kind {
	case "clicks":
		return s.clickStats(w, r, q, a)
	case "overview":
		return s.query(w, r, `SELECT count(*) AS events,count(CASE WHEN type='http_request' THEN 1 END) AS requests,count(CASE WHEN type IN ('page_view','route_change') THEN 1 END) AS pv,count(DISTINCT CASE WHEN visitor_id!='' THEN project_id||':'||visitor_id END) AS uv,count(DISTINCT CASE WHEN session_id!='' AND timestamp>`+strconv.FormatInt(Now()-1800000, 10)+` THEN project_id||':'||session_id END) AS active_sessions,count(CASE WHEN type IN ('js_error','unhandled_rejection') THEN 1 END) AS js_errors,COALESCE(avg(CASE WHEN type='page_hidden' THEN json_extract(properties,'$.durationMs') END),0) AS avg_duration_ms FROM events WHERE `+q, a...)
	case "trends":
		return s.query(w, r, `SELECT strftime('%Y-%m-%d %H:00',timestamp/1000,'unixepoch') AS time,count(*) AS events,count(CASE WHEN type='http_request' THEN 1 END) AS requests,count(CASE WHEN type IN ('page_view','route_change') THEN 1 END) AS pv,count(DISTINCT CASE WHEN visitor_id!='' THEN project_id||':'||visitor_id END) AS uv,count(CASE WHEN type='http_request' AND json_extract(properties,'$.status')>=400 THEN 1 END) AS errors FROM events WHERE `+q+` GROUP BY time ORDER BY time LIMIT 9000`, a...)
	case "devices":
		return s.pagedQuery(w, r, `SELECT device,browser,os,count(*) AS events,count(CASE WHEN type='http_request' THEN 1 END) AS requests,count(CASE WHEN type IN ('page_view','route_change') THEN 1 END) AS pv,count(DISTINCT CASE WHEN visitor_id!='' THEN project_id||':'||visitor_id END) AS uv FROM events WHERE `+q+` GROUP BY device,browser,os ORDER BY events DESC,device,browser,os LIMIT ? OFFSET ?`, a...)
	default:
		// Use a separate column name so SQLite cannot resolve GROUP BY to the
		// original pathname and accidentally merge all hash-router pages.
		return s.pagedQuery(w, r, `WITH page_events AS (
			SELECT type,CASE WHEN source_type='browser'
				THEN COALESCE(NULLIF(json_extract(properties,'$.pagePath'),''),path)
				ELSE path END AS page_path
			FROM events WHERE `+q+`
		)
		SELECT page_path AS path,count(*) AS events,
			count(CASE WHEN type='http_request' THEN 1 END) AS requests,
			count(CASE WHEN type IN ('page_view','route_change') THEN 1 END) AS pv
		FROM page_events GROUP BY page_path ORDER BY events DESC,page_path LIMIT ? OFFSET ?`, a...)
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
	timezone, err := s.systemTimezone(r.Context())
	if err != nil {
		return err
	}
	var size int64
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if f, e := os.Stat(s.cfg.DBPath + suffix); e == nil {
			size += f.Size()
		}
	}
	jsonOut(w, 200, map[string]any{"version": branding.Version(), "databaseBytes": size, "diskFreeBytes": diskFree(filepath.Dir(s.cfg.DBPath)), "offlineSeconds": s.cfg.OfflineSeconds, "publicUrl": s.cfg.PublicURL, "database": "SQLite WAL", "dockerMode": "authorized host collector (read only)", "timezone": timezone, "webUpdateEnabled": len(s.cfg.WebUpdateKey) != 0})
	return nil
}

func (s *Server) systemTimezone(ctx context.Context) (string, error) {
	var name string
	err := s.DB.QueryRowContext(ctx, `SELECT COALESCE((SELECT value FROM system_settings WHERE name='timezone'),'UTC')`).Scan(&name)
	return name, err
}

func (s *Server) setSystemTimezone(w http.ResponseWriter, r *http.Request) error {
	var input struct {
		Timezone string `json:"timezone"`
	}
	if err := decode(r, &input); err != nil {
		return err
	}
	if input.Timezone == "" || input.Timezone == "Local" || len(input.Timezone) > 100 {
		return fail(400, "请选择有效的时区")
	}
	if _, err := time.LoadLocation(input.Timezone); err != nil {
		return fail(400, "请选择有效的时区")
	}
	if _, err := s.DB.ExecContext(r.Context(), `INSERT INTO system_settings(name,value) VALUES('timezone',?)
		ON CONFLICT(name) DO UPDATE SET value=excluded.value`, input.Timezone); err != nil {
		return err
	}
	s.audit("system.timezone", input.Timezone)
	jsonOut(w, 200, input)
	return nil
}
