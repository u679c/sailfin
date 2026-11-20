package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sailfin/internal/core"
	"sailfin/internal/protocol"
	"strings"
	"testing"
	"time"
)

func TestFileOffsetsRotationAndAuthorization(t *testing.T) {
	dir := t.TempDir()
	logs := filepath.Join(dir, "logs")
	if e := os.Mkdir(logs, 0700); e != nil {
		t.Fatal(e)
	}
	a, e := Open(Options{Server: "http://127.0.0.1:8080", StateDir: filepath.Join(dir, "state"), LogRoots: []string{logs}})
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	a.state.ProjectID = "test"
	a.state.AgentID = "agent-test"
	path := filepath.Join(logs, "access.log")
	line := fmt.Sprintf("127.0.0.1 - - [%s] \"GET /real?secret=x HTTP/1.1\" 200 42 \"-\" \"test\"\n", time.Now().Format("02/Jan/2006:15:04:05 -0700"))
	if e = os.WriteFile(path, []byte(line), 0600); e != nil {
		t.Fatal(e)
	}
	src := protocol.Source{ID: "access", Path: path, Parser: "nginx"}
	if e = a.read(src); e != nil {
		t.Fatal(e)
	}
	if len(a.state.Pending) != 1 || a.state.Pending[0].Path != "/real" {
		t.Fatal(a.state.Pending)
	}
	if e = a.save(); e != nil {
		t.Fatal(e)
	}
	if e = a.read(src); e != nil {
		t.Fatal(e)
	}
	if len(a.state.Pending) != 1 {
		t.Fatal("offset replay")
	}
	if e = os.Rename(path, path+".1"); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, []byte(line), 0600); e != nil {
		t.Fatal(e)
	}
	if e = a.read(src); e != nil {
		t.Fatal(e)
	}
	if len(a.state.Pending) != 2 {
		t.Fatal("rotation not followed")
	}
	if a.state.Pending[0].EventID == a.state.Pending[1].EventID {
		t.Fatal("rotation ID collision")
	}
	secret := filepath.Join(dir, "secret")
	if e = os.WriteFile(secret, []byte("private"), 0600); e != nil {
		t.Fatal(e)
	}
	link := filepath.Join(logs, "escape")
	if e = os.Symlink(secret, link); e != nil {
		t.Fatal(e)
	}
	if _, e = a.authorized(link); e == nil {
		t.Fatal("symlink escaped authorization")
	}
	if _, e = Open(Options{Server: "http://127.0.0.1:8080", StateDir: a.opt.StateDir}); e == nil {
		t.Fatal("state lock missing")
	}
	if e = os.WriteFile(path, []byte(strings.Repeat(line, 1100)), 0600); e != nil {
		t.Fatal(e)
	}
	a.state.Cursors = map[string]Cursor{}
	a.state.Pending = nil
	if e = a.read(src); e != nil {
		t.Fatal(e)
	}
	if len(a.state.Pending) != 1000 {
		t.Fatal("queue not bounded", len(a.state.Pending))
	}
}
func TestRemoteHTTPSRequired(t *testing.T) {
	if _, e := Open(Options{Server: "http://remote.example", StateDir: t.TempDir()}); e == nil {
		t.Fatal("unsafe remote transport accepted")
	}
}
func TestDockerOperationsAreReadOnly(t *testing.T) {
	d := NewDocker("/does/not/exist", nil)
	if _, e := d.Logs(context.Background(), "not-authorized", 0); e == nil {
		t.Fatal("unauthorized Docker read accepted")
	}
}
func TestLocalDocker(t *testing.T) {
	socket := os.Getenv("SAILFIN_DOCKER_SOCKET")
	if socket == "" {
		t.Skip("set SAILFIN_DOCKER_SOCKET for actual local Docker read-only verification")
	}
	d := NewDocker(socket, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	rows, e := d.Discover(ctx)
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("Discovered %d real containers without mutation", len(rows))
}

// Route actual HTTP requests into Core without opening network ports or a browser.
type coreTransport struct {
	handler http.Handler
	offline bool
}

func (tr *coreTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if tr.offline {
		return nil, errors.New("test-induced connection outage")
	}
	w := httptest.NewRecorder()
	r.RemoteAddr = "127.0.0.1:2000"
	tr.handler.ServeHTTP(w, r)
	res := w.Result()
	res.Request = r
	return res, nil
}
func TestActualCoreAgentPipelineAndRecovery(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, e := core.Open(core.Config{DBPath: filepath.Join(dir, "core.db"), AdminPassword: "integration-test-only-password"})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	project := "integration"
	_, e = s.DB.Exec("INSERT INTO projects(id,name,description,created_at) VALUES(?,?,?,?)", project, "test site", "", time.Now().UnixMilli())
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.DB.Exec("INSERT INTO enrollments VALUES(?,?,?,?)", eventID("enroll-test-token"), project, "real-host", time.Now().Add(time.Minute).UnixMilli())
	if e != nil {
		t.Fatal(e)
	}
	stateDir := filepath.Join(dir, "state")
	opts := Options{Server: "http://127.0.0.1:8080", StateDir: stateDir, EnrollToken: "enroll-test-token", LogRoots: []string{dir}}
	a, e := Open(opts)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	tr := &coreTransport{handler: s.Handler()}
	a.client.Transport = tr
	if e = a.register(ctx); e != nil {
		t.Fatal(e)
	}
	a.syncConfig(ctx)
	a.heartbeat(ctx)
	var host string
	if e = s.DB.QueryRow("SELECT hostname FROM agents WHERE id=?", a.state.AgentID).Scan(&host); e != nil || host == "" {
		t.Fatal("heartbeat not persisted", e)
	}
	path := filepath.Join(dir, "actual-access.jsonl")
	line := fmt.Sprintf("{\"path\":\"/real?token=private\",\"method\":\"GET\",\"status\":200,\"timestamp\":%d}\n", time.Now().UnixMilli())
	if e = os.WriteFile(path, []byte(line), 0600); e != nil {
		t.Fatal(e)
	}
	a.state.Config.Sources = []protocol.Source{{ID: "access", Path: path, Parser: "json"}}
	if e = a.cycle(ctx); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = s.DB.QueryRow("SELECT count(*) FROM events WHERE project_id=? AND path='/real'", project).Scan(&count); e != nil || count != 1 {
		t.Fatal("file to Core failed", e, count)
	}
	f, e := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.WriteString(line)
	f.Close()
	if e != nil {
		t.Fatal(e)
	}
	if e = a.read(a.state.Config.Sources[0]); e != nil {
		t.Fatal(e)
	}
	if e = a.save(); e != nil {
		t.Fatal(e)
	}
	tr.offline = true
	if e = a.flush(ctx); e == nil || len(a.state.Pending) != 1 {
		t.Fatal("outage lost pending data")
	}
	agentID := a.state.AgentID
	a.Close()
	a.lock = nil
	opts.EnrollToken = ""
	resumed, e := Open(opts)
	if e != nil {
		t.Fatal(e)
	}
	defer resumed.Close()
	resumed.client.Transport = tr
	if resumed.state.AgentID != agentID || len(resumed.state.Pending) != 1 {
		t.Fatal("credentials/spool not restored")
	}
	tr.offline = false
	if e = resumed.flush(ctx); e != nil {
		t.Fatal(e)
	}
	if e = s.DB.QueryRow("SELECT count(*) FROM events WHERE project_id=?", project).Scan(&count); e != nil || count != 2 {
		t.Fatal("recovery failed", e, count)
	}
}
