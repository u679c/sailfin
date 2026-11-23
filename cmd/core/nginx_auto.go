package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"sailfin/internal/agent"
	"sailfin/internal/branding"
	"sailfin/internal/core"
)

type nginxAuto struct {
	mu       sync.Mutex
	relay    string
	keyPath  string
	authPath string
	key      string
	active   atomic.Bool
	ready    atomic.Bool
	pending  *nginxAutoPlan
}

type nginxAutoPlan struct {
	view      core.NginxPreview
	binary    string
	instances []nginxInstance
	files     []*deploymentFile
	expires   time.Time
}

func newNginxAuto(dataDir, relay string) (*nginxAuto, error) {
	if relay == "" {
		relay = "127.0.0.1:8091"
	}
	cleanAddress, err := loopbackAddress(relay)
	if err != nil {
		return nil, fmt.Errorf("invalid probe relay address: %w", err)
	}
	absDir, err := filepath.Abs(dataDir)
	if err != nil || strings.ContainsAny(absDir, "\r\n") {
		return nil, errors.New("invalid Nginx relay state directory")
	}
	c := &nginxAuto{
		relay: cleanAddress, keyPath: filepath.Join(absDir, ".nginx-relay-key"),
		authPath: filepath.Join(absDir, ".nginx-relay-auth.conf"),
	}
	key, err := os.ReadFile(c.keyPath)
	if os.IsNotExist(err) {
		c.key = core.Random()
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	decoded, err := base64.RawURLEncoding.DecodeString(string(key))
	if err != nil || len(decoded) != 32 {
		return nil, errors.New("invalid saved Nginx relay key")
	}
	c.key = string(key)
	c.active.Store(true)
	return c, nil
}

// A per-installation key authenticates the Host metadata set by our Nginx block.
// The browser can't choose this header or learn the key from the SDK.
func (c *nginxAuto) browserSite(r *http.Request) (agent.Site, bool) {
	if !c.active.Load() || subtle.ConstantTimeCompare([]byte(branding.Header(r, "Auto")), []byte(c.key)) != 1 {
		return agent.Site{}, false
	}
	origin := branding.Header(r, "Origin")
	u, err := url.Parse(origin)
	if err != nil || u.Hostname() == "" || len(u.Host) > 120 || u.User != nil ||
		(u.Scheme != "http" && u.Scheme != "https") || origin != u.Scheme+"://"+u.Host {
		return agent.Site{}, false
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port != "" && !(u.Scheme == "http" && port == "80") && !(u.Scheme == "https" && port == "443") {
		host += ":" + port
	}
	return agent.Site{
		ID: autoNginxSiteID(host), Name: strings.ToLower(u.Host),
		BrowserOrigin: origin, RequireConsent: !isLoopbackHost(u.Hostname()),
	}, true
}

func autoNginxSiteID(host string) string {
	key := sha256.Sum256([]byte(host))
	return fmt.Sprintf("nginx-%x", key[:16])
}

func (c *nginxAuto) Scan(ctx context.Context) (core.NginxPreview, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending = nil
	binary, running, warnings, err := runningNginx(ctx)
	if err != nil {
		return core.NginxPreview{}, err
	}
	plan := &nginxAutoPlan{
		view:   core.NginxPreview{ID: core.Random(), Sites: []core.NginxSiteResult{}},
		binary: binary, expires: time.Now().Add(10 * time.Minute),
	}
	if len(running) == 0 && len(warnings) == 0 {
		warnings = append(warnings, "未发现运行中的 Nginx 主进程")
	}
	var instances []nginxInstance
	owners := map[string]int{}
	for _, master := range running {
		dump, err := runNginx(ctx, binary, master.args, "-T")
		if err == nil {
			err = inspectNginx(ctx, binary, &master, dump)
		}
		if err != nil {
			warnings = append(warnings, c.safeError(err))
			continue
		}
		for _, path := range master.files {
			owners[path]++
		}
		instances = append(instances, master)
	}
	for _, warning := range warnings {
		plan.view.Sites = append(plan.view.Sites, core.NginxSiteResult{Site: "Nginx 实例", Status: "skipped", Reason: warning})
	}
	seen := map[string]bool{}
	for _, master := range instances {
		modified := false
		for _, path := range master.files {
			if seen[path] {
				continue
			}
			seen[path] = true
			file, err := readDeploymentFile(path, false)
			if err != nil {
				plan.view.Sites = append(plan.view.Sites, core.NginxSiteResult{Site: "配置文件", File: path, Status: "skipped", Reason: c.safeError(err)})
				continue
			}
			if !bytes.Contains(file.before, []byte("server")) {
				continue
			}
			next, reports, err := planAutomaticInjection(string(file.before), path, c.relay, c.authPath)
			if err != nil {
				plan.view.Sites = append(plan.view.Sites, core.NginxSiteResult{Site: "配置文件", File: path, Status: "skipped", Reason: c.safeError(err)})
				continue
			}
			blocked := ""
			if owners[path] != 1 {
				blocked = "配置文件由多个 Nginx 实例共用，自动接入已跳过"
			} else if next != string(file.before) && !writableConfig(path) {
				blocked = "当前用户没有修改配置文件或所在目录的权限"
			}
			for _, report := range reports {
				if report.Status == "ready" {
					if report.SiteID == "" {
						report.Status, report.Reason = "skipped", "请手动添加没有单一明确域名的站点"
					} else if blocked != "" {
						report.Status, report.Reason = "skipped", blocked
					} else {
						plan.view.Ready++
					}
				}
				plan.view.Sites = append(plan.view.Sites, report)
			}
			if blocked == "" && next != string(file.before) {
				file.after = []byte(next)
				plan.files = append(plan.files, &file)
				modified = true
			}
		}
		if modified {
			plan.instances = append(plan.instances, master)
		}
	}
	if len(plan.view.Sites) == 0 {
		plan.view.Sites = append(plan.view.Sites, core.NginxSiteResult{Site: "Nginx", Status: "skipped", Reason: "没有发现可识别的 HTTP 站点"})
	}
	c.pending = plan
	return plan.view, nil
}

func (c *nginxAuto) Apply(ctx context.Context, id string, ids []string) (core.NginxPreview, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	plan := c.pending
	if plan == nil || id == "" || plan.view.ID != id || time.Now().After(plan.expires) {
		return core.NginxPreview{}, errors.New("预览已失效，请重新扫描")
	}
	if len(plan.files) == 0 {
		return core.NginxPreview{}, errors.New("没有待接入的站点")
	}
	if !c.ready.Load() {
		return core.NginxPreview{}, errors.New("本机探针的转发端口未就绪，请查看探针状态")
	}
	available := map[string]int{}
	for _, row := range plan.view.Sites {
		if row.Status == "ready" {
			available[row.SiteID]++
		}
	}
	selected := map[string]bool{}
	for _, id := range ids {
		if available[id] == 0 || selected[id] {
			return core.NginxPreview{}, errors.New("所选站点不在预览中，请重新扫描")
		}
		selected[id] = true
	}
	var files []*deploymentFile
	for _, file := range plan.files {
		next, _, err := planHostInjection(string(file.before), file.path, c.relay, c.authPath, selected)
		if err != nil {
			return core.NginxPreview{}, err
		}
		if next != string(file.before) {
			file.after = []byte(next)
			files = append(files, file)
		}
	}
	if len(files) == 0 {
		return core.NginxPreview{}, errors.New("请选择可接入的站点")
	}
	plan.files = files
	c.pending = nil
	for _, master := range plan.instances {
		if err := verifyNginxMaster(master); err != nil {
			return core.NginxPreview{}, err
		}
		dump, err := runNginx(ctx, plan.binary, master.args, "-T")
		if err != nil {
			return core.NginxPreview{}, errors.New(c.safeError(err))
		}
		for _, file := range plan.files {
			if containsPath(master.files, file.path) && !nginxIncludesFile(string(dump), file.path) {
				return core.NginxPreview{}, errors.New("Nginx 加载的配置已改变，请重新扫描")
			}
		}
	}
	if err := c.persistKey(); err != nil {
		return core.NginxPreview{}, err
	}
	if err := c.persistAuthInclude(); err != nil {
		return core.NginxPreview{}, err
	}
	var written []*deploymentFile
	var reloaded []nginxInstance
	rollback := func(cause error) (core.NginxPreview, error) {
		for i := len(written) - 1; i >= 0; i-- {
			if err := written[i].restore(); err != nil {
				cause = errors.Join(cause, fmt.Errorf("恢复失败 %s（备份 %s）: %w", written[i].path, written[i].backup, err))
			}
		}
		for _, master := range reloaded {
			if err := verifyNginxMaster(master); err != nil {
				cause = errors.Join(cause, err)
				continue
			}
			if _, err := runNginx(context.Background(), plan.binary, master.args, "-s", "reload"); err != nil {
				cause = errors.Join(cause, err)
			}
		}
		return core.NginxPreview{}, errors.New(c.safeError(cause))
	}
	for _, file := range plan.files {
		if err := file.apply(); err != nil {
			return rollback(err)
		}
		written = append(written, file)
	}
	// Validate every affected instance before requesting any reload.
	for _, master := range plan.instances {
		if _, err := runNginx(ctx, plan.binary, master.args, "-t"); err != nil {
			return rollback(err)
		}
	}
	for _, master := range plan.instances {
		if err := verifyNginxMaster(master); err != nil {
			return rollback(err)
		}
		if _, err := runNginx(ctx, plan.binary, master.args, "-s", "reload"); err != nil {
			return rollback(err)
		}
		reloaded = append(reloaded, master)
	}
	plan.view.Ready = 0
	for i := range plan.view.Sites {
		report := &plan.view.Sites[i]
		if report.Status == "ready" && selected[report.SiteID] {
			report.Status = "applied"
			report.Reason = "配置校验通过，已请求重载"
		} else if report.Status == "ready" {
			report.Status, report.Reason = "skipped", "本次未选择，可重新扫描后接入"
		}
	}
	return plan.view, nil
}

func writableConfig(path string) bool {
	// Access is checked again by the atomic writer when applying the plan.
	return syscall.Access(path, 2) == nil && syscall.Access(filepath.Dir(path), 2) == nil
}

func containsPath(paths []string, path string) bool {
	for _, candidate := range paths {
		if candidate == path {
			return true
		}
	}
	return false
}

func (c *nginxAuto) persistKey() error {
	if c.active.Load() {
		return nil
	}
	f, err := os.OpenFile(c.keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = f.WriteString(c.key); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(c.keyPath)
		return err
	}
	c.active.Store(true)
	return nil
}

func (c *nginxAuto) persistAuthInclude() error {
	body := []byte(fmt.Sprintf("proxy_set_header X-Sailfin-Auto \"%s\";\n", c.key))
	file, err := readDeploymentFile(c.authPath, true)
	if err != nil {
		return err
	}
	if file.exists {
		if !bytes.Equal(file.before, body) || file.mode != 0600 {
			return errors.New("本机 Nginx 转发授权文件已变化，请检查探针数据目录")
		}
		return nil
	}
	file.mode = 0600
	return file.replace(body)
}

func (c *nginxAuto) safeError(err error) string {
	return strings.ReplaceAll(err.Error(), c.key, "[redacted]")
}
