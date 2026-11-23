package agent

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sailfin/internal/branding"
	"sailfin/internal/parser"
	"sailfin/internal/protocol"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

type Options struct {
	Server, StateDir, EnrollToken string
	LogRoots                      []string
	DockerSocket                  string
	DockerAllowed                 []string
	AllowHTTP                     bool
	Sites                         []Site
	RelayAddress                  string
	AutoBrowserSite               func(*http.Request) (Site, bool)
	RelayState                    func(bool)
}
type Cursor struct {
	Offset  int64  `json:"offset"`
	Inode   uint64 `json:"inode"`
	Dropped bool   `json:"dropped"`
}
type State struct {
	AgentID     string            `json:"agentId"`
	ProjectID   string            `json:"projectId"`
	Token       string            `json:"token"`
	Cursors     map[string]Cursor `json:"cursors"`
	Pending     []protocol.Event  `json:"pending"`
	DockerSince map[string]int64  `json:"dockerSince"`
	Config      protocol.Config   `json:"config"`
	Dropped     int64             `json:"dropped"`
}
type Agent struct {
	opt        Options
	state      State
	client     *http.Client
	docker     *Docker
	lastErr    string
	lock       *os.File
	sites      map[string]Site
	local      bool
	relaySites atomic.Pointer[relaySites]
}

type relaySites struct {
	sites   map[string]Site
	enabled map[string]bool
}
type HTTPError struct {
	Status int
	Body   string
}

func (e HTTPError) Error() string { return fmt.Sprintf("Core returned %d: %s", e.Status, e.Body) }
func Open(o Options) (*Agent, error) {
	return open(o, "")
}

func open(o Options, localID string) (*Agent, error) {
	sites, err := indexSites(o.Sites)
	if err != nil {
		return nil, err
	}
	if localID != "" {
		// The URL is a request namespace for the in-process transport.
		o.Server = "http://sailfin.internal"
	} else if err := validateServer(o); err != nil {
		return nil, err
	}
	var e error
	if o.StateDir == "" {
		o.StateDir = "var/agent"
	}
	if e = os.MkdirAll(o.StateDir, 0700); e != nil {
		return nil, e
	}
	lock, e := os.OpenFile(filepath.Join(o.StateDir, "agent.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		lock.Close()
		return nil, errors.New("agent state directory already in use")
	}
	a := &Agent{opt: o, client: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}, lock: lock, sites: sites, local: localID != ""}
	a.relaySites.Store(&relaySites{sites: sites})
	good := false
	defer func() {
		if !good {
			a.Close()
		}
	}()
	raw, e := os.ReadFile(filepath.Join(o.StateDir, "state.json"))
	if e == nil {
		if e = json.Unmarshal(raw, &a.state); e != nil {
			return nil, e
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	if a.local {
		if a.state.AgentID != "" && (a.state.AgentID != localID || a.state.ProjectID != "" || a.state.Token != "") {
			return nil, errors.New("local probe requires a separate state directory")
		}
		a.state.AgentID = localID
	}
	if a.state.Cursors == nil {
		a.state.Cursors = map[string]Cursor{}
	}
	if a.state.DockerSince == nil {
		a.state.DockerSince = map[string]int64{}
	}
	if len(a.state.Pending) > 1000 {
		return nil, errors.New("spool exceeds 1000 event limit")
	}
	if o.DockerSocket != "" {
		a.docker = NewDocker(o.DockerSocket, o.DockerAllowed)
	}
	good = true
	return a, nil
}
func validateServer(o Options) error {
	u, err := url.Parse(o.Server)
	if err != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return errors.New("invalid Core URL")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || o.AllowHTTP)) {
		return errors.New("remote Core requires HTTPS (explicit SAILFIN_ALLOW_HTTP only for isolated development)")
	}
	return nil
}

func (a *Agent) Close() {
	if a.lock != nil {
		_ = syscall.Flock(int(a.lock.Fd()), syscall.LOCK_UN)
		a.lock.Close()
	}
}
func (a *Agent) save() error {
	b, e := json.Marshal(a.state)
	if e != nil {
		return e
	}
	path := filepath.Join(a.opt.StateDir, "state.json")
	tmp := path + ".tmp"
	f, e := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if e != nil {
		return e
	}
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return os.Rename(tmp, path)
}
func (a *Agent) request(ctx context.Context, method, path string, body, out any) error {
	var b io.Reader
	if body != nil {
		raw, e := json.Marshal(body)
		if e != nil {
			return e
		}
		b = bytes.NewReader(raw)
	}
	r, e := http.NewRequestWithContext(ctx, method, strings.TrimRight(a.opt.Server, "/")+path, b)
	if e != nil {
		return e
	}
	r.Header.Set("Content-Type", "application/json")
	if a.state.Token != "" {
		r.Header.Set("Authorization", "Bearer "+a.state.Token)
	}
	res, e := a.client.Do(r)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 2000))
		return HTTPError{res.StatusCode, string(raw)}
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(res.Body, 262144)).Decode(out)
	}
	return nil
}
func (a *Agent) register(ctx context.Context) error {
	if a.state.Token == "" {
		if a.opt.EnrollToken == "" {
			return errors.New("first start requires SAILFIN_ENROLL_TOKEN")
		}
		var reply struct {
			AgentID   string `json:"agentId"`
			Token     string `json:"token"`
			ProjectID string `json:"projectId"`
		}
		if e := a.request(ctx, "POST", "/api/v1/agent/enroll", map[string]string{"token": a.opt.EnrollToken}, &reply); e != nil {
			return e
		}
		a.state.AgentID = reply.AgentID
		a.state.Token = reply.Token
		a.state.ProjectID = reply.ProjectID
		if e := a.save(); e != nil {
			return e
		}
	}
	return nil
}
func (a *Agent) Run(ctx context.Context) error {
	if a.local && len(a.opt.Sites) != 0 {
		if err := a.request(ctx, http.MethodPost, "/api/v1/agent/site-sources", a.opt.Sites, nil); err != nil {
			return fmt.Errorf("load existing local site sources: %w", err)
		}
	}
	if !a.local {
		if err := a.registerWithRetry(ctx); err != nil {
			return err
		}
	}
	relay, err := a.startRelay()
	if err != nil {
		return err
	}
	if relay != nil {
		defer relay.Close()
		if a.opt.RelayState != nil {
			a.opt.RelayState(true)
			defer a.opt.RelayState(false)
		}
	}
	slog.Info("Sailfin probe started", "agentId", a.state.AgentID)
	return a.collect(ctx)
}

func (a *Agent) registerWithRetry(ctx context.Context) error {
	if a.opt.EnrollToken == "" && a.state.Token == "" {
		return errors.New("first start requires SAILFIN_ENROLL_TOKEN")
	}
	if len(a.sites) != 0 && a.state.ProjectID != "" {
		return errors.New("site collection requires a host enrollment and its own state directory")
	}
	delay := time.Second
	for {
		e := a.register(ctx)
		if e == nil {
			break
		}
		var h HTTPError
		if errors.As(e, &h) && h.Status < 500 {
			return e
		}
		slog.Warn("Core unavailable during registration; retrying", "error", e)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		if delay < 30*time.Second {
			delay *= 2
		}
	}
	if len(a.sites) != 0 && a.state.ProjectID != "" {
		return errors.New("use a host enrollment token, not a project enrollment token")
	}
	return nil
}

func (a *Agent) collect(ctx context.Context) error {
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()
	collect := time.NewTicker(time.Second)
	defer collect.Stop()
	a.syncConfig(ctx)
	a.heartbeat(ctx)
	last := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return a.save()
		case <-heartbeat.C:
			a.syncConfig(ctx)
			a.heartbeat(ctx)
		case <-collect.C:
			interval := a.state.Config.IntervalSeconds
			if interval < 1 {
				interval = 5
			}
			if time.Since(last) < time.Duration(interval)*time.Second {
				continue
			}
			last = time.Now()
			if a.local {
				a.syncConfig(ctx)
			}
			if !a.state.Config.Enabled {
				continue
			}
			if e := a.cycle(ctx); e != nil {
				a.lastErr = e.Error()
				slog.Warn("collection cycle failed", "error", e)
			}
		}
	}
}
func (a *Agent) syncConfig(ctx context.Context) {
	var c protocol.Config
	if e := a.request(ctx, "GET", "/api/v1/agent/config", nil, &c); e != nil {
		a.lastErr = e.Error()
		a.state.Config.Enabled = false
		return
	}
	a.state.Config = c
	if a.local {
		if c.SiteEnabled != nil {
			sites, err := indexSites(c.LocalSites)
			if err != nil {
				a.lastErr = err.Error()
				return
			}
			a.opt.Sites = c.LocalSites
			a.sites = sites
			a.relaySites.Store(&relaySites{sites: sites, enabled: c.SiteEnabled})
		}
		return
	}
	if e := a.save(); e != nil {
		a.lastErr = e.Error()
	}
}
func (a *Agent) heartbeat(ctx context.Context) {
	if a.local {
		if err := a.request(ctx, http.MethodPost, "/api/v1/agent/site-logs", siteLogPaths(a.opt.LogRoots), nil); err != nil {
			slog.Warn("could not update authorized site logs", "error", err)
		}
	}
	host, _ := os.Hostname()
	containers := []protocol.Container{}
	if a.docker != nil {
		v, e := a.docker.Discover(ctx)
		if e != nil {
			a.lastErr = e.Error()
		} else {
			containers = v
		}
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	values := map[string]any{
		"heapBytes": m.Alloc, "buffered": len(a.state.Pending),
		"dropped": a.state.Dropped, "configVersion": a.state.Config.Version,
	}
	if a.local {
		values["configuredSites"] = len(a.sites)
	}
	metrics, _ := json.Marshal(values)
	b := protocol.Heartbeat{Hostname: host, OS: runtime.GOOS, Arch: runtime.GOARCH, Version: branding.Version(), Containers: containers, Metrics: metrics, Error: a.lastErr}
	if e := a.request(ctx, "POST", "/api/v1/agent/heartbeat", b, nil); e != nil {
		slog.Warn("heartbeat failed", "error", e)
	}
}
func (a *Agent) cycle(ctx context.Context) error {
	if e := a.flush(ctx); e != nil {
		return e
	}
	a.lastErr = ""
	if a.state.ProjectID == "" {
		hasLogs := false
		for _, site := range a.opt.Sites {
			if site.AccessLog == "" || !a.siteEnabled(site.ID) {
				continue
			}
			hasLogs = true
			if len(a.state.Pending) >= 900 {
				break
			}
			source := protocol.Source{ID: site.ID, Path: a.sites[site.ID].AccessLog, Parser: "nginx"}
			if err := a.readForSite(source, site.ID); err != nil {
				a.lastErr = err.Error()
			}
		}
		if !hasLogs {
			return nil
		}
		if err := a.save(); err != nil {
			return err
		}
		return a.flush(ctx)
	}
	for _, src := range a.state.Config.Sources {
		if len(a.state.Pending) >= 900 {
			break
		}
		if e := a.read(src); e != nil {
			a.lastErr = e.Error()
		}
	}
	if a.docker != nil {
		for _, id := range a.state.Config.DockerTargets {
			if len(a.state.Pending) >= 900 {
				break
			}
			since := a.state.DockerSince[id]
			if since == 0 {
				since = time.Now().Add(-time.Minute).Unix()
			}
			lines, e := a.docker.Logs(ctx, id, since)
			if e != nil {
				a.lastErr = e.Error()
				continue
			}
			for _, line := range lines {
				stamp, content, ok := strings.Cut(line, " ")
				if !ok {
					continue
				}
				t, e := time.Parse(time.RFC3339Nano, stamp)
				if e != nil {
					continue
				}
				kind := "nginx"
				if strings.HasPrefix(strings.TrimSpace(content), "{") {
					kind = "json"
				}
				r, e := parser.Parse(kind, content, nil)
				if e != nil || parser.Validate(r) != nil {
					a.state.Dropped++
					continue
				}
				if len(a.state.Pending) >= 1000 {
					break
				}
				a.state.Pending = append(a.state.Pending, parser.Event(r, a.state.ProjectID, eventID(id+line)))
				if t.Unix() > a.state.DockerSince[id] {
					a.state.DockerSince[id] = t.Unix()
				}
			}
		}
	}
	if e := a.save(); e != nil {
		return e
	}
	return a.flush(ctx)
}

func (a *Agent) siteEnabled(id string) bool {
	enabled := a.relaySites.Load().enabled
	return enabled == nil || enabled[protocol.SiteProjectID(id)]
}
func (a *Agent) flush(ctx context.Context) error {
	size := a.state.Config.BatchSize
	if size < 1 || size > 100 {
		size = 50
	}
	for len(a.state.Pending) > 0 {
		n := len(a.state.Pending)
		if n > size {
			n = size
		}
		path := "/api/v1/agent/events/batch"
		var body any
		if a.state.ProjectID == "" {
			siteID := a.state.Pending[0].ProjectID
			site, ok := a.sites[siteID]
			if !ok || site.AccessLog == "" {
				return fmt.Errorf("restore site %s in the site config to send its pending log events", siteID)
			}
			for i := 1; i < n; i++ {
				if a.state.Pending[i].ProjectID != siteID {
					n = i
					break
				}
			}
			path = "/api/v1/agent/sites/batch"
			body = protocol.SiteBatch{SiteID: siteID, Name: site.Name, Source: "host", Events: a.state.Pending[:n]}
		} else {
			body = map[string]any{"events": a.state.Pending[:n]}
		}
		e := a.request(ctx, "POST", path, body, nil)
		if e != nil {
			var h HTTPError
			if errors.As(e, &h) && (h.Status == 400 || h.Status == 413) {
				if n > 1 {
					size = 1
					continue
				}
				a.state.Dropped += int64(n)
				a.state.Pending = a.state.Pending[n:]
				if se := a.save(); se != nil {
					return se
				}
				a.lastErr = "rejected events discarded: " + h.Body
				continue
			}
			return e
		}
		a.state.Pending = a.state.Pending[n:]
		if e = a.save(); e != nil {
			return e
		}
	}
	return nil
}
func eventID(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }
func (a *Agent) authorized(path string) (string, error) {
	real, e := filepath.EvalSymlinks(path)
	if e != nil {
		return "", e
	}
	for _, root := range a.opt.LogRoots {
		abs, e := filepath.Abs(root)
		if e != nil {
			continue
		}
		abs, e = filepath.EvalSymlinks(abs)
		if e != nil {
			continue
		}
		rel, e := filepath.Rel(abs, real)
		if e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return real, nil
		}
	}
	return "", fmt.Errorf("source is outside locally authorized log roots: %s", path)
}
func (a *Agent) read(src protocol.Source) error {
	return a.readForSite(src, a.state.ProjectID)
}

func (a *Agent) readForSite(src protocol.Source, projectID string) error {
	path, e := a.authorized(src.Path)
	if e != nil {
		return e
	}
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return e
	}
	if !st.Mode().IsRegular() {
		return errors.New("source must be regular file")
	}
	inode := uint64(0)
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		inode = sys.Ino
	}
	key := src.ID + ":" + path
	c := a.state.Cursors[key]
	if c.Inode != inode || st.Size() < c.Offset {
		c = Cursor{Inode: inode}
	}
	if _, e = f.Seek(c.Offset, io.SeekStart); e != nil {
		return e
	}
	reader := bufio.NewReaderSize(f, 65536)
	cycleStart := c.Offset
	for len(a.state.Pending) < 1000 && c.Offset-cycleStart < 1048576 {
		start := c.Offset
		line, err := reader.ReadSlice('\n')
		if err == io.EOF {
			break
		}
		if err == bufio.ErrBufferFull {
			c.Offset += int64(len(line))
			c.Dropped = true
			continue
		}
		if err != nil {
			return err
		}
		c.Offset += int64(len(line))
		if c.Dropped {
			a.state.Dropped++
			c.Dropped = false
			continue
		}
		r, e := parser.Parse(src.Parser, strings.TrimSpace(string(line)), src.Fields)
		if e != nil || parser.Validate(r) != nil {
			a.state.Dropped++
			continue
		}
		id := eventID(fmt.Sprintf("%s:%s:%d:%d:%s", a.state.AgentID, key, inode, start, line))
		if a.state.ProjectID == "" && strings.HasPrefix(r.Path, relayPrefix) {
			continue
		}
		a.state.Pending = append(a.state.Pending, parser.Event(r, projectID, id))
	}
	a.state.Cursors[key] = c
	return nil
}
