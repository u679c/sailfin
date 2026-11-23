package core

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sailfin/internal/protocol"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testPassword = "test-only-password-12345"

type harness struct {
	s      *Server
	t      *testing.T
	cookie *http.Cookie
	csrf   string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	s, e := Open(Config{DBPath: filepath.Join(t.TempDir(), "core.db"), AdminPassword: testPassword})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	h := &harness{s: s, t: t}
	rr := h.call("POST", "/auth/login", map[string]any{"name": "admin", "password": testPassword}, "", 200)
	for _, c := range rr.Result().Cookies() {
		if c.Name == "sailfin_session" {
			h.cookie = c
		}
	}
	var v map[string]string
	_ = json.Unmarshal(rr.Body.Bytes(), &v)
	h.csrf = v["csrf"]
	return h
}
func (h *harness) call(method, path string, body any, token string, status int) *httptest.ResponseRecorder {
	h.t.Helper()
	var b io.Reader
	if body != nil {
		raw, e := json.Marshal(body)
		if e != nil {
			h.t.Fatal(e)
		}
		b = bytes.NewReader(raw)
	}
	r := httptest.NewRequest(method, "/api/v1"+path, b)
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("Content-Type", "application/json")
	if h.cookie != nil {
		r.AddCookie(h.cookie)
		r.Header.Set("X-CSRF-Token", h.csrf)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if strings.HasPrefix(path, "/collect/browser") || strings.HasPrefix(path, "/collection/") {
		r.Header.Set("Origin", "http://localhost:5174")
	}
	w := httptest.NewRecorder()
	h.s.Handler().ServeHTTP(w, r)
	if w.Code != status {
		h.t.Fatalf("%s %s: status=%d expected=%d body=%s", method, path, w.Code, status, w.Body.String())
	}
	return w
}
func object(rr *httptest.ResponseRecorder) map[string]any {
	var v map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &v)
	return v
}
func list(rr *httptest.ResponseRecorder) []map[string]any {
	var v []map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &v)
	return v
}
func (h *harness) project(name string, quota int) string {
	rr := h.call("POST", "/projects", map[string]any{"name": name, "description": "test fixture", "domains": []string{"http://localhost:5174"}, "browserEnabled": true, "dailyQuota": quota}, "", 200)
	return object(rr)["id"].(string)
}
func (h *harness) agent(p string) map[string]any {
	rr := h.call("POST", "/agents/enrollment", map[string]string{"projectId": p, "name": "test-host"}, "", 201)
	token := object(rr)["token"].(string)
	result := object(h.call("POST", "/agent/enroll", map[string]string{"token": token}, "", 201))
	h.call("POST", "/agent/enroll", map[string]string{"token": token}, "", 401)
	return result
}
func TestCollectionLifecycleAndIsolation(t *testing.T) {
	h := newHarness(t)
	p := h.project("site one", 100)
	other := h.project("site two", 100)
	a := h.agent(p)
	id := a["agentId"].(string)
	token := a["token"].(string)
	h.call("POST", "/agent/heartbeat", protocol.Heartbeat{Hostname: "actual-test-host", OS: "linux", Arch: "amd64", Version: "test", Containers: []protocol.Container{}, Metrics: json.RawMessage(`{"buffered":0}`)}, token, 200)
	rows := list(h.call("GET", "/agents/"+id, nil, "", 200))
	if rows[0]["status"] != "Online" {
		t.Fatalf("agent not online: %v", rows)
	}
	config := protocol.Config{Enabled: true, Sources: []protocol.Source{{ID: "access", Path: "/tmp/access.log", Parser: "nginx"}}, BatchSize: 20, IntervalSeconds: 5}
	h.call("PUT", "/agents/"+id+"/config", config, "", 200)
	cfg := object(h.call("GET", "/agent/config", nil, token, 200))
	if cfg["version"] != float64(2) {
		t.Fatal(cfg)
	}
	ev := protocol.Event{EventID: "request-1", ProjectID: p, EventType: "http_request", Timestamp: Now(), Path: "/home", UA: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/125.0.0.0 Safari/537.36", Properties: map[string]any{"status": float64(200), "method": "GET"}}
	batch := map[string]any{"events": []protocol.Event{ev}}
	h.call("POST", "/agent/events/batch", batch, token, 200)
	if object(h.call("POST", "/agent/events/batch", batch, token, 200))["duplicates"] != float64(1) {
		t.Fatal("dedupe failed")
	}
	browser := []protocol.Event{}
	for i, typ := range []string{"page_view", "click", "page_hidden"} {
		e := protocol.Event{EventID: typ, ProjectID: p, SessionID: "session-one", VisitorID: "visitor-one", EventType: typ, Timestamp: Now() - 1000 + int64(i), Path: "/home", Properties: map[string]any{}}
		if typ == "page_hidden" {
			e.Properties["durationMs"] = float64(5000)
		}
		browser = append(browser, e)
	}
	h.call("POST", "/collect/browser?projectId="+p, map[string]any{"events": browser}, "", 200)
	stats := list(h.call("GET", "/stats/overview?projectId="+p, nil, "", 200))[0]
	if stats["pv"] != float64(1) || stats["requests"] != float64(1) || stats["uv"] != float64(1) || stats["events"] != float64(4) {
		t.Fatal(stats)
	}
	if len(list(h.call("GET", "/sessions/session-one?projectId="+p, nil, "", 200))) != 3 {
		t.Fatal("timeline mismatch")
	}
	if len(list(h.call("GET", "/sessions/session-one?projectId="+other, nil, "", 200))) != 0 {
		t.Fatal("session leaked")
	}
	ev.EventID = "cross-project"
	ev.ProjectID = other
	h.call("POST", "/agent/events/batch", map[string]any{"events": []protocol.Event{ev}}, token, 400)
	if _, e := h.s.DB.Exec("UPDATE agents SET last_seen=? WHERE id=?", Now()-100000, id); e != nil {
		t.Fatal(e)
	}
	if list(h.call("GET", "/agents/"+id, nil, "", 200))[0]["status"] != "Offline" {
		t.Fatal("offline timeout failed")
	}
	h.call("PATCH", "/agents/"+id, map[string]any{"enabled": false}, "", 200)
	h.call("POST", "/agent/heartbeat", protocol.Heartbeat{}, token, 401)
	h.call("PATCH", "/agents/"+id, map[string]any{"enabled": true}, "", 200)
	h.call("GET", "/agent/config", nil, token, 200)
	h.call("POST", "/system/backup", nil, "", 200)
}
func TestSecurityBoundaries(t *testing.T) {
	h := newHarness(t)
	p := h.project("limited", 1)
	a := h.agent(p)
	token := a["token"].(string)
	r := httptest.NewRequest("GET", "/api/v1/projects", nil)
	w := httptest.NewRecorder()
	h.s.Handler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	r = httptest.NewRequest("PATCH", "/api/v1/projects/"+p, strings.NewReader(`{}`))
	r.AddCookie(h.cookie)
	w = httptest.NewRecorder()
	h.s.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("missing CSRF accepted")
	}
	h.call("POST", "/agent/heartbeat", protocol.Heartbeat{}, "invalid", 401)
	for i := 0; i < 2; i++ {
		ev := protocol.Event{EventID: string(rune('a' + i)), ProjectID: p, EventType: "http_request", Path: "/safe", Timestamp: Now(), Properties: map[string]any{}}
		want := 200
		if i == 1 {
			want = 429
		}
		h.call("POST", "/agent/events/batch", map[string]any{"events": []protocol.Event{ev}}, token, want)
	}
	ev := protocol.Event{EventID: "bad-property", ProjectID: p, EventType: "http_request", Path: "/safe", Timestamp: Now(), Properties: map[string]any{"password": "secret"}}
	h.call("POST", "/agent/events/batch", map[string]any{"events": []protocol.Event{ev}}, token, 400)
	ev.Properties = nil
	ev.Path = "/x?token=secret"
	h.call("POST", "/agent/events/batch", map[string]any{"events": []protocol.Event{ev}}, token, 400)
	ev.Path = "/safe"
	ev.Timestamp = time.Now().Add(-32 * 24 * time.Hour).UnixMilli()
	h.call("POST", "/agent/events/batch", map[string]any{"events": []protocol.Event{ev}}, token, 400)
	r = httptest.NewRequest("POST", "/api/v1/collect/browser?projectId="+p, strings.NewReader(`{"events":[]}`))
	r.Header.Set("Origin", "https://untrusted.example")
	w = httptest.NewRecorder()
	h.s.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("untrusted origin accepted")
	}
	r = httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"name":"`+strings.Repeat("x", 270000)+`"}`))
	r.RemoteAddr = "10.0.0.2:100"
	w = httptest.NewRecorder()
	h.s.Handler().ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("oversized JSON accepted")
	}
	rows := list(h.call("GET", "/events?projectId=%27%20OR%201=1--", nil, "", 200))
	if len(rows) != 0 {
		t.Fatal("SQL injection")
	}
	// Same origin alone never grants project write access or bypasses quotas.
	h.call("POST", "/collect/browser?projectId="+p, map[string]any{"events": []protocol.Event{{EventID: "browser", ProjectID: p, SessionID: "s", VisitorID: "v", EventType: "click", Path: "/", Timestamp: Now()}}}, "", 429)
}
func TestPersistenceAndRetention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "persistent.db")
	s, e := Open(Config{DBPath: path, AdminPassword: testPassword})
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.DB.Exec("INSERT INTO projects(id,name,description,created_at) VALUES('persistent','name','',?)", Now())
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
	s, e = Open(Config{DBPath: path})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	var n int
	if e = s.DB.QueryRow("SELECT count(*) FROM projects WHERE id='persistent'").Scan(&n); e != nil || n != 1 {
		t.Fatal("data not persisted", e)
	}
	s.cleanup()
}

func TestPasswordChangeAndExpiredEnrollment(t *testing.T) {
	h := newHarness(t)
	p := h.project("expiry", 100)
	rr := h.call("POST", "/agents/enrollment", map[string]string{"projectId": p, "name": "expired"}, "", 201)
	token := object(rr)["token"].(string)
	if _, e := h.s.DB.Exec("UPDATE enrollments SET expires=0"); e != nil {
		t.Fatal(e)
	}
	h.call("POST", "/agent/enroll", map[string]string{"token": token}, "", 401)
	const newPassword = "short-test"
	h.call("POST", "/auth/password", map[string]string{"currentPassword": testPassword, "newPassword": ""}, "", 400)
	h.call("POST", "/auth/password", map[string]string{"currentPassword": "wrong", "newPassword": newPassword}, "", 403)
	h.call("POST", "/auth/password", map[string]string{"currentPassword": testPassword, "newPassword": newPassword}, "", 200)
	h.call("POST", "/auth/login", map[string]string{"name": "admin", "password": testPassword}, "", 401)
	h.call("POST", "/auth/login", map[string]string{"name": "admin", "password": newPassword}, "", 200)
}

func BenchmarkIngest100(b *testing.B) {
	s, e := Open(Config{DBPath: filepath.Join(b.TempDir(), "bench.db"), AdminPassword: testPassword})
	if e != nil {
		b.Fatal(e)
	}
	defer s.Close()
	if _, e = s.DB.Exec("INSERT INTO projects(id,name,description,created_at) VALUES('bench','benchmark','',?)", Now()); e != nil {
		b.Fatal(e)
	}
	events := make([]protocol.Event, 100)
	for i := range events {
		events[i] = protocol.Event{EventID: strconv.Itoa(i), ProjectID: "bench", EventType: "http_request", Timestamp: Now(), Path: "/bench", UA: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/125.0.0.0 Safari/537.36", Properties: map[string]any{"status": float64(200), "method": "GET"}}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		if _, e = s.DB.Exec("DELETE FROM events; DELETE FROM daily_usage"); e != nil {
			b.Fatal(e)
		}
		b.StartTimer()
		r := httptest.NewRequest("POST", "/api/v1/agent/events/batch", nil)
		w := httptest.NewRecorder()
		if e = s.ingest(w, r, "bench", "host", "bench-agent", events); e != nil {
			b.Fatal(e)
		}
	}
	b.ReportMetric(100, "events/op")
}

func TestManagementHistoryAndDeletion(t *testing.T) {
	h := newHarness(t)
	p := h.project("remove-me", 100)
	keep := h.project("keep-me", 100)
	a := h.agent(p)
	id := a["agentId"].(string)
	token := a["token"].(string)
	if len(list(h.call("GET", "/projects/"+p+"/config", nil, "", 200))) != 1 {
		t.Fatal("project policy history missing")
	}
	h.call("PATCH", "/projects/"+p, map[string]any{"name": "remove-me", "description": "", "domains": []string{"http://localhost:5174"}, "browserEnabled": true, "collection": map[string]any{"sampleRate": 0.5}}, "", 200)
	if len(list(h.call("GET", "/projects/"+p+"/config", nil, "", 200))) != 2 {
		t.Fatal("policy version not appended")
	}
	h.call("DELETE", "/agents/"+id, map[string]string{"confirmName": "wrong"}, "", 400)
	h.call("DELETE", "/agents/"+id, map[string]string{"confirmName": "test-host"}, "", 200)
	h.call("GET", "/agent/config", nil, token, 401)
	h.call("DELETE", "/projects/"+p, map[string]string{"confirmName": "wrong"}, "", 400)
	h.call("DELETE", "/projects/"+p, map[string]string{"confirmName": "remove-me"}, "", 200)
	if len(list(h.call("GET", "/projects/"+keep, nil, "", 200))) != 1 {
		t.Fatal("deletion crossed project boundary")
	}
	if len(list(h.call("GET", "/projects/"+p+"/config", nil, "", 200))) != 0 {
		t.Fatal("orphan project history")
	}
	summary := object(h.call("GET", "/security/summary", nil, "", 200))
	if _, ok := summary["diskFreeBytes"]; !ok {
		t.Fatal("capacity status missing")
	}
}

func TestNginxScanManagementOrigins(t *testing.T) {
	h := newHarness(t)
	cases := []struct {
		configured, origin string
		want               bool
	}{
		{"http://localhost:5173", "http://localhost:5173", true},
		{"http://localhost:5173", "http://127.0.0.1:5173", true},
		{"http://127.0.0.1:5173", "http://[::1]:5173", true},
		{"http://localhost:5173", "http://localhost:5174", false},
		{"http://localhost:5173", "https://localhost:5173", false},
		{"http://localhost:5173", "http://localhost.example:5173", false},
		{"http://localhost:5173", "http://127.0.0.1:5173/elsewhere", false},
		{"https://trace.example", "https://trace.example", true},
		{"https://trace.example", "https://localhost", false},
	}
	for _, tt := range cases {
		h.s.cfg.PublicURL = tt.configured
		r := httptest.NewRequest(http.MethodPost, "/api/v1/system/nginx/scan", nil)
		r.AddCookie(h.cookie)
		r.Header.Set("Origin", tt.origin)
		r.Header.Set("X-CSRF-Token", h.csrf)
		err := h.s.authorize(r)
		if (err == nil) != tt.want {
			t.Errorf("origin %q with management URL %q: want authorized=%t, got %v", tt.origin, tt.configured, tt.want, err)
		}
	}
	h.s.cfg.PublicURL = "http://localhost:5173"
	r := httptest.NewRequest(http.MethodPost, "/api/v1/system/nginx/scan", nil)
	r.AddCookie(h.cookie)
	r.Header.Set("Origin", "http://127.0.0.1:5173")
	if h.s.authorize(r) == nil {
		t.Fatal("a loopback alias must still require a CSRF token")
	}
}
