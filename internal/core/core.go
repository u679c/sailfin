package core

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/crypto/argon2"
	"tracelite/internal/protocol"
)

//go:embed schema.sql
var schema string

type Config struct {
	Address, DBPath, PublicURL, WebDir, AdminName, AdminPassword, BootstrapPath string
	OfflineSeconds                                                              int
	LocalOnly                                                                   bool
	Nginx                                                                       NginxIntegration
}
type bucket struct {
	Start time.Time
	Count int
}
type Server struct {
	DB     *sql.DB
	cfg    Config
	mux    *http.ServeMux
	mu     sync.Mutex
	limits map[string]bucket
	slots  chan struct{}
}
type apiError struct {
	Status  int
	Message string
}

func (e apiError) Error() string        { return e.Message }
func fail(status int, msg string) error { return apiError{status, msg} }
func Now() int64                        { return time.Now().UnixMilli() }
func Random() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func passwordHash(p string) string {
	salt := make([]byte, 16)
	if _, e := rand.Read(salt); e != nil {
		panic(e)
	}
	key := argon2.IDKey([]byte(p), salt, 2, 32*1024, 2, 32)
	return base64.RawStdEncoding.EncodeToString(salt) + "." + base64.RawStdEncoding.EncodeToString(key)
}
func passwordOK(p, encoded string) bool {
	a, b, ok := strings.Cut(encoded, ".")
	if !ok {
		return false
	}
	salt, e := base64.RawStdEncoding.DecodeString(a)
	if e != nil {
		return false
	}
	want, e := base64.RawStdEncoding.DecodeString(b)
	if e != nil {
		return false
	}
	key := argon2.IDKey([]byte(p), salt, 2, 32*1024, 2, 32)
	return subtle.ConstantTimeCompare(key, want) == 1
}
func Open(c Config) (*Server, error) {
	if c.Address == "" {
		c.Address = "127.0.0.1:8080"
	}
	if c.DBPath == "" {
		c.DBPath = "var/tracelite.db"
	}
	if c.PublicURL == "" {
		c.PublicURL = "http://localhost:5173"
	}
	if c.AdminName == "" {
		c.AdminName = "admin"
	}
	c.PublicURL = strings.TrimRight(c.PublicURL, "/")
	public, e := url.Parse(c.PublicURL)
	if e != nil || public.Host == "" || (public.Scheme != "http" && public.Scheme != "https") || public.Path != "" || public.RawQuery != "" || public.Fragment != "" || public.User != nil {
		return nil, errors.New("TRACELITE_PUBLIC_URL must be an HTTP(S) origin")
	}
	if c.OfflineSeconds < 0 {
		return nil, errors.New("offline threshold must be positive")
	}
	if c.OfflineSeconds == 0 {
		c.OfflineSeconds = 90
	}
	if c.BootstrapPath == "" {
		c.BootstrapPath = filepath.Join(filepath.Dir(c.DBPath), "bootstrap-admin.txt")
	}
	if err := os.MkdirAll(filepath.Dir(c.DBPath), 0700); err != nil {
		return nil, err
	}
	// Restrict a newly created database before SQLite opens it (including WAL siblings).
	f, e := os.OpenFile(c.DBPath, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	f.Close()
	if e = os.Chmod(c.DBPath, 0600); e != nil {
		return nil, e
	}
	dsn := (&url.URL{Scheme: "file", Path: mustAbs(c.DBPath)}).String() + "?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on&_synchronous=NORMAL"
	db, e := sql.Open("sqlite3", dsn)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	good := false
	defer func() {
		if !good {
			db.Close()
		}
	}()
	tx, e := db.Begin()
	if e != nil {
		return nil, e
	}
	if _, e = tx.Exec(schema); e != nil {
		tx.Rollback()
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	var n int
	if e = db.QueryRow("SELECT count(*) FROM users").Scan(&n); e != nil {
		return nil, e
	}
	if n == 0 {
		p := c.AdminPassword
		if p == "" {
			p = Random()
			if e = os.WriteFile(c.BootstrapPath, []byte("username: "+c.AdminName+"\npassword: "+p+"\n"), 0600); e != nil {
				return nil, e
			}
			slog.Info("Administrator initialized; read private credential file", "path", c.BootstrapPath)
		}
		if len(p) < 12 {
			return nil, errors.New("admin password requires at least 12 characters")
		}
		if _, e = db.Exec("INSERT INTO users(id,name,password) VALUES(?,?,?)", Random(), c.AdminName, passwordHash(p)); e != nil {
			return nil, e
		}
	}
	s := &Server{DB: db, cfg: c, mux: http.NewServeMux(), limits: make(map[string]bucket), slots: make(chan struct{}, 32)}
	if c.LocalOnly {
		if e = s.initProbe(); e != nil {
			return nil, e
		}
	}
	s.routes()
	good = true
	return s, nil
}
func mustAbs(p string) string {
	v, e := filepath.Abs(p)
	if e != nil {
		panic(e)
	}
	return v
}
func (s *Server) Close() error { return s.DB.Close() }
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'")
		select {
		case s.slots <- struct{}{}:
			defer func() { <-s.slots }()
		default:
			s.reject(w, r, fail(503, "server busy"))
			return
		}
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		if ip == "" {
			ip = r.RemoteAddr
		}
		limit := 240
		if strings.HasPrefix(r.URL.Path, "/api/v1/collect") {
			limit = 60
		}
		if !s.rate(ip, limit) {
			s.count("rate_limited")
			s.reject(w, r, fail(429, "rate limit exceeded"))
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 262144)
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		s.mux.ServeHTTP(w, r.WithContext(ctx))
	})
}
func (s *Server) rate(key string, max int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	b := s.limits[key]
	if now.Sub(b.Start) > time.Minute {
		b = bucket{Start: now}
	}
	if len(s.limits) >= 10000 {
		for k, v := range s.limits {
			if now.Sub(v.Start) > time.Minute {
				delete(s.limits, k)
			}
		}
		if _, ok := s.limits[key]; !ok && len(s.limits) >= 10000 {
			return false
		}
	}
	b.Count++
	s.limits[key] = b
	return b.Count <= max
}
func (s *Server) count(name string) {
	_, e := s.DB.Exec("INSERT INTO security_counters VALUES(?,1) ON CONFLICT(name) DO UPDATE SET count=count+1", name)
	if e != nil {
		slog.Warn("counter write failed", "error", e)
	}
}
func (s *Server) audit(action, target string) {
	_, e := s.DB.Exec("INSERT INTO audit_logs(action,target,created_at) VALUES(?,?,?)", action, target, Now())
	if e != nil {
		slog.Warn("audit failed", "error", e)
	}
}
func jsonOut(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (s *Server) reject(w http.ResponseWriter, r *http.Request, e error) {
	var a apiError
	if errors.As(e, &a) {
		jsonOut(w, a.Status, map[string]string{"error": a.Message})
		return
	}
	slog.Error("request failed", "path", r.URL.Path, "error", e)
	jsonOut(w, 500, map[string]string{"error": "internal error; inspect Core logs"})
}
func decode(r *http.Request, v any) error {
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return fail(400, "invalid JSON: "+e.Error())
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return fail(400, "request must contain one JSON object")
	}
	return nil
}
func (s *Server) route(pattern string, auth bool, fn func(http.ResponseWriter, *http.Request) error) {
	s.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if auth {
			if e := s.authorize(r); e != nil {
				s.reject(w, r, e)
				return
			}
		}
		if e := fn(w, r); e != nil {
			s.reject(w, r, e)
		}
	})
}
func (s *Server) authorize(r *http.Request) error {
	c, e := r.Cookie("tracelite_session")
	if e != nil {
		return fail(401, "login required")
	}
	var csrf string
	if e = s.DB.QueryRowContext(r.Context(), "SELECT csrf FROM auth_sessions WHERE token=? AND expires>?", hash(c.Value), Now()).Scan(&csrf); e != nil {
		return fail(401, "session expired")
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		if subtle.ConstantTimeCompare([]byte(csrf), []byte(r.Header.Get("X-CSRF-Token"))) != 1 {
			return fail(403, "invalid CSRF token")
		}
		if origin := r.Header.Get("Origin"); origin != "" && !trustedManagementOrigin(s.cfg.PublicURL, origin) {
			return fail(403, "untrusted origin: use the management URL "+s.cfg.PublicURL)
		}
	}
	return nil
}
func (s *Server) rows(ctx context.Context, q string, args ...any) ([]map[string]any, error) {
	rs, e := s.DB.QueryContext(ctx, q, args...)
	if e != nil {
		return nil, e
	}
	defer rs.Close()
	cols, e := rs.Columns()
	if e != nil {
		return nil, e
	}
	out := []map[string]any{}
	for rs.Next() {
		v := make([]any, len(cols))
		ptr := make([]any, len(cols))
		for i := range v {
			ptr[i] = &v[i]
		}
		if e = rs.Scan(ptr...); e != nil {
			return nil, e
		}
		m := map[string]any{}
		for i, c := range cols {
			if b, ok := v[i].([]byte); ok {
				m[c] = string(b)
			} else {
				m[c] = v[i]
			}
		}
		out = append(out, m)
	}
	return out, rs.Err()
}
func (s *Server) query(w http.ResponseWriter, r *http.Request, q string, args ...any) error {
	v, e := s.rows(r.Context(), q, args...)
	if e != nil {
		return e
	}
	jsonOut(w, 200, v)
	return nil
}
func (s *Server) routes() {
	s.route("GET /api/v1/health", false, func(w http.ResponseWriter, r *http.Request) error {
		if e := s.DB.PingContext(r.Context()); e != nil {
			return e
		}
		jsonOut(w, 200, map[string]string{"status": "ok"})
		return nil
	})
	s.route("POST /api/v1/auth/login", false, s.login)
	s.route("POST /api/v1/auth/password", true, s.changePassword)
	s.route("GET /api/v1/auth/me", true, func(w http.ResponseWriter, r *http.Request) error {
		c, _ := r.Cookie("tracelite_session")
		return s.query(w, r, "SELECT u.name,u.role,a.csrf FROM auth_sessions a JOIN users u ON u.id=a.user_id WHERE a.token=?", hash(c.Value))
	})
	s.route("POST /api/v1/auth/logout", true, func(w http.ResponseWriter, r *http.Request) error {
		c, _ := r.Cookie("tracelite_session")
		_, e := s.DB.ExecContext(r.Context(), "DELETE FROM auth_sessions WHERE token=?", hash(c.Value))
		http.SetCookie(w, &http.Cookie{Name: "tracelite_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: strings.HasPrefix(s.cfg.PublicURL, "https://")})
		jsonOut(w, 200, map[string]bool{"ok": true})
		return e
	})
	s.route("GET /api/v1/sites", true, s.collectedSites)
	if s.cfg.LocalOnly {
		s.route("GET /api/v1/system/probe", true, s.probeStatus)
		s.route("PATCH /api/v1/system/probe", true, s.setProbeEnabled)
		if s.cfg.Nginx != nil {
			s.route("POST /api/v1/system/nginx/scan", true, s.scanNginx)
			s.route("POST /api/v1/system/nginx/apply", true, s.applyNginx)
		}
	} else {
		s.route("POST /api/v1/agent/sites/batch", false, s.siteEvents)
		s.route("GET /api/v1/projects", true, func(w http.ResponseWriter, r *http.Request) error {
			return s.query(w, r, "SELECT * FROM projects ORDER BY created_at DESC LIMIT 500")
		})
		s.route("POST /api/v1/projects", true, s.saveProject)
		s.route("DELETE /api/v1/projects/{id}", true, s.deleteProject)
		s.route("GET /api/v1/projects/{id}/config", true, s.projectHistory)
		s.route("DELETE /api/v1/agents/{id}", true, s.deleteAgent)
		s.route("PATCH /api/v1/projects/{id}", true, s.saveProject)
		s.route("GET /api/v1/projects/{id}", true, func(w http.ResponseWriter, r *http.Request) error {
			return s.query(w, r, "SELECT * FROM projects WHERE id=?", r.PathValue("id"))
		})
		s.route("POST /api/v1/agents/enrollment", true, s.enrollment)
		s.route("POST /api/v1/agent/enroll", false, s.enroll)
		s.route("POST /api/v1/agent/heartbeat", false, s.heartbeat)
		s.route("GET /api/v1/agent/config", false, s.agentConfig)
		s.route("POST /api/v1/agent/events/batch", false, s.agentEvents)
		s.route("POST /api/v1/agent/metrics", false, s.heartbeat)
		s.route("GET /api/v1/agents", true, s.agents)
		s.route("GET /api/v1/agents/{id}", true, s.agents)
		s.route("PATCH /api/v1/agents/{id}", true, s.patchAgent)
		s.route("GET /api/v1/agents/{id}/logs", true, func(w http.ResponseWriter, r *http.Request) error {
			return s.query(w, r, "SELECT * FROM agent_logs WHERE agent_id=? ORDER BY id DESC LIMIT 100", r.PathValue("id"))
		})
		s.route("GET /api/v1/agents/{id}/metrics", true, func(w http.ResponseWriter, r *http.Request) error {
			return s.query(w, r, "SELECT metrics,last_seen,error FROM agents WHERE id=?", r.PathValue("id"))
		})
		s.route("GET /api/v1/agents/{id}/config", true, s.configHistory)
		s.route("PUT /api/v1/agents/{id}/config", true, s.saveAgentConfig)
		s.route("POST /api/v1/collect/browser", false, s.browserEvents)
		s.route("OPTIONS /api/v1/collect/browser", false, s.browserOptions)
		s.route("GET /api/v1/collection/config", false, s.browserConfig)
		s.mux.Handle("/sdk/", http.StripPrefix("/sdk/", http.FileServer(http.Dir("browser-agent/dist"))))
	}
	s.route("GET /api/v1/events", true, s.events)
	s.route("GET /api/v1/sessions", true, s.sessions)
	s.route("GET /api/v1/sessions/{id}", true, s.sessionDetail)
	for _, kind := range []string{"overview", "trends", "devices", "pages"} {
		k := kind
		s.route("GET /api/v1/stats/"+k, true, func(w http.ResponseWriter, r *http.Request) error { return s.stats(w, r, k) })
	}
	s.route("GET /api/v1/security/summary", true, s.security)
	s.route("GET /api/v1/system/status", true, s.system)
	s.route("POST /api/v1/system/backup", true, s.backup)
	if s.cfg.WebDir != "" {
		s.mux.Handle("/", dashboardFiles(s.cfg.WebDir))
	}
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if e := decode(r, &b); e != nil {
		return e
	}
	if len(b.Name) > 120 || len(b.Password) > 256 {
		return fail(400, "credentials exceed length limit")
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if !s.rate("login:"+ip, 10) {
		return fail(429, "too many login attempts; retry in one minute")
	}
	var id, encoded string
	e := s.DB.QueryRowContext(r.Context(), "SELECT id,password FROM users WHERE name=?", b.Name).Scan(&id, &encoded)
	if e != nil || !passwordOK(b.Password, encoded) {
		s.count("authentication_failed")
		return fail(401, "invalid username or password")
	}
	tok, csrf := Random(), Random()
	_, e = s.DB.ExecContext(r.Context(), "INSERT INTO auth_sessions VALUES(?,?,?,?)", hash(tok), id, csrf, Now()+int64(12*time.Hour/time.Millisecond))
	if e != nil {
		return e
	}
	http.SetCookie(w, &http.Cookie{Name: "tracelite_session", Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: strings.HasPrefix(s.cfg.PublicURL, "https://"), MaxAge: 43200})
	s.audit("login", b.Name)
	jsonOut(w, 200, map[string]string{"csrf": csrf, "name": b.Name})
	return nil
}

type projectInput struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Domains     []string       `json:"domains"`
	Status      string         `json:"status"`
	Retention   int            `json:"retentionDays"`
	Quota       int            `json:"dailyQuota"`
	Browser     bool           `json:"browserEnabled"`
	Collection  map[string]any `json:"collection"`
}

func (s *Server) saveProject(w http.ResponseWriter, r *http.Request) error {
	var p projectInput
	if e := decode(r, &p); e != nil {
		return e
	}
	if len(p.Name) < 1 || len(p.Name) > 120 || len(p.Description) > 2000 || len(p.Domains) > 30 {
		return fail(400, "invalid project fields")
	}
	if p.Status == "" {
		p.Status = "active"
	}
	if p.Status != "active" && p.Status != "paused" && p.Status != "archived" {
		return fail(400, "invalid project status")
	}
	if p.Retention == 0 {
		p.Retention = 30
	}
	if p.Quota == 0 {
		p.Quota = 10000
	}
	if p.Retention < 1 || p.Retention > 365 || p.Quota < 1 || p.Quota > 10000000 {
		return fail(400, "invalid retention or quota")
	}
	for _, domain := range p.Domains {
		u, e := url.Parse(domain)
		if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.Path != "" || u.RawQuery != "" || u.User != nil || u.Fragment != "" {
			return fail(400, "domains must be exact origins, e.g. http://localhost:5174")
		}
	}
	if e := validateCollection(p.Collection); e != nil {
		return e
	}
	domains, _ := json.Marshal(p.Domains)
	collection, _ := json.Marshal(p.Collection)
	id := r.PathValue("id")
	tx, e := s.DB.BeginTx(r.Context(), nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if id == "" {
		id = Random()[:16]
		_, e = tx.ExecContext(r.Context(), "INSERT INTO projects(id,name,description,domains,status,retention_days,daily_quota,browser_enabled,collection,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)", id, p.Name, p.Description, string(domains), p.Status, p.Retention, p.Quota, p.Browser, string(collection), Now())
	} else {
		var res sql.Result
		res, e = tx.ExecContext(r.Context(), "UPDATE projects SET name=?,description=?,domains=?,status=?,retention_days=?,daily_quota=?,browser_enabled=?,collection=? WHERE id=?", p.Name, p.Description, string(domains), p.Status, p.Retention, p.Quota, p.Browser, string(collection), id)
		if e == nil {
			n, _ := res.RowsAffected()
			if n == 0 {
				return fail(404, "project not found")
			}
		}
	}
	if e != nil {
		return e
	}
	if _, e = tx.Exec("INSERT INTO project_configs(project_id,version,config,created_at) SELECT ?,COALESCE(max(version),0)+1,?,? FROM project_configs WHERE project_id=?", id, string(collection), Now(), id); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	s.audit("project.save", id)
	jsonOut(w, 200, map[string]string{"id": id})
	return nil
}
func validateCollection(m map[string]any) error {
	allowed := map[string]bool{"eventTypes": true, "sampleRate": true, "excludePaths": true, "excludeElements": true, "batchSize": true, "intervalSeconds": true}
	for k, v := range m {
		if !allowed[k] {
			return fail(400, "unsupported collection field: "+k)
		}
		switch k {
		case "sampleRate":
			n, ok := v.(float64)
			if !ok || n < 0 || n > 1 {
				return fail(400, "sampleRate must be 0..1")
			}
		case "batchSize", "intervalSeconds":
			n, ok := v.(float64)
			if !ok || n < 1 || n > 100 {
				return fail(400, "batch size and interval must be 1..100")
			}
		default:
			a, ok := v.([]any)
			if !ok || len(a) > 50 {
				return fail(400, "collection lists require at most 50 strings")
			}
			for _, x := range a {
				t, ok := x.(string)
				if !ok || len(t) > 200 {
					return fail(400, "invalid collection list entry")
				}
			}
		}
	}
	return nil
}
func (s *Server) enrollment(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		ProjectID string `json:"projectId"`
		Name      string `json:"name"`
	}
	if e := decode(r, &b); e != nil {
		return e
	}
	var n int
	if e := s.DB.QueryRowContext(r.Context(), "SELECT count(*) FROM projects WHERE id=? AND status='active'", b.ProjectID).Scan(&n); e != nil {
		return e
	}
	if n != 1 || len(b.Name) < 1 || len(b.Name) > 120 {
		return fail(400, "active project and agent name required")
	}
	token := Random()
	expires := Now() + 600000
	_, e := s.DB.ExecContext(r.Context(), "INSERT INTO enrollments VALUES(?,?,?,?)", hash(token), b.ProjectID, b.Name, expires)
	if e != nil {
		return e
	}
	s.audit("agent.enrollment", b.ProjectID)
	jsonOut(w, 201, map[string]any{"token": token, "expiresAt": expires})
	return nil
}
func (s *Server) enroll(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		Token string `json:"token"`
	}
	if e := decode(r, &b); e != nil {
		return e
	}
	tx, e := s.DB.BeginTx(r.Context(), nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var project, name string
	e = tx.QueryRow("DELETE FROM enrollments WHERE token=? AND expires>? RETURNING project_id,name", hash(b.Token), Now()).Scan(&project, &name)
	if e != nil {
		s.countAfterRollback(tx, "authentication_failed")
		return fail(401, "expired, used or invalid enrollment token")
	}
	id, token := Random()[:16], Random()
	_, e = tx.Exec("INSERT INTO agents(id,project_id,name,token,created_at) VALUES(?,?,?,?,?)", id, project, name, hash(token), Now())
	if e != nil {
		return e
	}
	cfg := protocol.Config{Version: 1, Enabled: true, Sources: []protocol.Source{}, DockerTargets: []string{}, BatchSize: 50, IntervalSeconds: 5}
	raw, _ := json.Marshal(cfg)
	if _, e = tx.Exec("INSERT INTO agent_configs VALUES(?,?,?,?)", id, 1, string(raw), Now()); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	s.audit("agent.enroll", id)
	jsonOut(w, 201, map[string]string{"agentId": id, "token": token, "projectId": project})
	return nil
}
func (s *Server) countAfterRollback(tx *sql.Tx, name string) { _ = tx.Rollback(); s.count(name) }
func (s *Server) agentIdentity(r *http.Request) (string, string, error) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return "", "", fail(401, "agent credential required")
	}
	var id, project string
	e := s.DB.QueryRowContext(r.Context(), "SELECT a.id,a.project_id FROM agents a LEFT JOIN projects p ON p.id=a.project_id WHERE a.token=? AND a.enabled=1 AND (a.project_id='' OR p.status='active')", hash(strings.TrimPrefix(h, "Bearer "))).Scan(&id, &project)
	if e != nil {
		s.count("authentication_failed")
		return "", "", fail(401, "invalid or disabled agent")
	}
	return id, project, nil
}
func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) error {
	id, _, e := s.agentIdentity(r)
	if e != nil {
		return e
	}
	return s.recordHeartbeat(w, r, id)
}
func (s *Server) recordHeartbeat(w http.ResponseWriter, r *http.Request, id string) error {
	var b protocol.Heartbeat
	if e := decode(r, &b); e != nil {
		return e
	}
	if len(b.Hostname) > 200 || len(b.Error) > 1000 || len(b.Containers) > 200 || len(b.Metrics) > 8192 {
		return fail(400, "heartbeat exceeds limits")
	}
	raw, _ := json.Marshal(b.Containers)
	metrics := string(b.Metrics)
	if metrics == "" {
		metrics = "{}"
	}
	_, e := s.DB.ExecContext(r.Context(), "UPDATE agents SET hostname=?,os=?,arch=?,version=?,last_seen=?,metrics=?,containers=?,error=? WHERE id=?", b.Hostname, b.OS, b.Arch, b.Version, Now(), metrics, string(raw), b.Error, id)
	if e != nil {
		return e
	}
	if b.Error != "" {
		_, e = s.DB.ExecContext(r.Context(), "INSERT INTO agent_logs(agent_id,message,created_at) VALUES(?,?,?)", id, b.Error, Now())
		if e != nil {
			return e
		}
	}
	jsonOut(w, 200, map[string]any{"ok": true, "serverTime": Now()})
	return nil
}
func (s *Server) agentConfig(w http.ResponseWriter, r *http.Request) error {
	id, _, e := s.agentIdentity(r)
	if e != nil {
		return e
	}
	var raw string
	if e = s.DB.QueryRowContext(r.Context(), "SELECT config FROM agent_configs WHERE agent_id=? ORDER BY version DESC LIMIT 1", id).Scan(&raw); e != nil {
		return e
	}
	w.Header().Set("Content-Type", "application/json")
	_, e = w.Write([]byte(raw))
	return e
}
func (s *Server) agents(w http.ResponseWriter, r *http.Request) error {
	q := `SELECT a.id,a.project_id,a.name,a.enabled,a.hostname,a.os,a.arch,a.version,a.last_seen,a.metrics,a.containers,a.error,a.created_at,CASE WHEN a.enabled=0 THEN 'Disabled' WHEN a.last_seen=0 THEN 'Pending Registration' WHEN a.last_seen<? THEN 'Offline' WHEN a.error!='' THEN 'Warning' ELSE 'Online' END AS status,(SELECT count(*) FROM events e WHERE e.agent_id=a.id AND e.received_at>=?) AS today_events FROM agents a WHERE (?='' OR a.id=?) AND (?='' OR a.project_id=?) ORDER BY a.created_at DESC LIMIT 500`
	id := r.PathValue("id")
	p := r.URL.Query().Get("projectId")
	now := time.Now().UTC()
	mid := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).UnixMilli()
	return s.query(w, r, q, Now()-int64(s.cfg.OfflineSeconds)*1000, mid, id, id, p, p)
}
func (s *Server) patchAgent(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		Enabled bool `json:"enabled"`
		Revoke  bool `json:"revoke"`
	}
	if e := decode(r, &b); e != nil {
		return e
	}
	id := r.PathValue("id")
	token := ""
	if b.Revoke {
		token = hash(Random())
	}
	res, e := s.DB.ExecContext(r.Context(), "UPDATE agents SET enabled=?,token=CASE WHEN ?='' THEN token ELSE ? END WHERE id=?", b.Enabled, token, token, id)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fail(404, "agent not found")
	}
	s.audit("agent.permissions", id)
	jsonOut(w, 200, map[string]bool{"ok": true})
	return nil
}
func (s *Server) configHistory(w http.ResponseWriter, r *http.Request) error {
	return s.query(w, r, "SELECT * FROM agent_configs WHERE agent_id=? ORDER BY version DESC LIMIT 20", r.PathValue("id"))
}
func (s *Server) saveAgentConfig(w http.ResponseWriter, r *http.Request) error {
	var c protocol.Config
	if e := decode(r, &c); e != nil {
		return e
	}
	if len(c.Sources) > 20 || len(c.DockerTargets) > 20 || c.BatchSize < 1 || c.BatchSize > 100 || c.IntervalSeconds < 1 || c.IntervalSeconds > 300 {
		return fail(400, "invalid collection limits")
	}
	seen := map[string]bool{}
	for _, src := range c.Sources {
		if src.ID == "" || seen[src.ID] || !filepath.IsAbs(src.Path) {
			return fail(400, "source needs unique ID and absolute authorized path")
		}
		seen[src.ID] = true
		switch src.Parser {
		case "nginx", "apache", "caddy", "json", "docker-json":
		default:
			return fail(400, "unknown parser")
		}
	}
	id := r.PathValue("id")
	tx, e := s.DB.BeginTx(r.Context(), nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var n int
	if e = tx.QueryRow("SELECT count(*) FROM agents WHERE id=?", id).Scan(&n); e != nil {
		return e
	}
	if n == 0 {
		return fail(404, "agent not found")
	}
	if e = tx.QueryRow("SELECT COALESCE(max(version),0)+1 FROM agent_configs WHERE agent_id=?", id).Scan(&c.Version); e != nil {
		return e
	}
	raw, _ := json.Marshal(c)
	if _, e = tx.Exec("INSERT INTO agent_configs VALUES(?,?,?,?)", id, c.Version, string(raw), Now()); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	s.audit("agent.config", id)
	jsonOut(w, 200, c)
	return nil
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		Current string `json:"currentPassword"`
		New     string `json:"newPassword"`
	}
	if e := decode(r, &b); e != nil {
		return e
	}
	if len(b.New) < 12 || len(b.New) > 256 || len(b.Current) > 256 {
		return fail(400, "new password must contain 12..256 characters")
	}
	c, _ := r.Cookie("tracelite_session")
	var id, encoded string
	if e := s.DB.QueryRowContext(r.Context(), "SELECT u.id,u.password FROM users u JOIN auth_sessions a ON a.user_id=u.id WHERE a.token=?", hash(c.Value)).Scan(&id, &encoded); e != nil {
		return e
	}
	if !passwordOK(b.Current, encoded) {
		s.count("authentication_failed")
		return fail(403, "current password is incorrect")
	}
	newHash := passwordHash(b.New)
	tx, e := s.DB.BeginTx(r.Context(), nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("UPDATE users SET password=? WHERE id=?", newHash, id); e != nil {
		return e
	}
	if _, e = tx.Exec("DELETE FROM auth_sessions WHERE user_id=? AND token!=?", id, hash(c.Value)); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	s.audit("auth.password", id)
	jsonOut(w, 200, map[string]bool{"ok": true})
	return nil
}
