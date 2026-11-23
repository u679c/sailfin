package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"sailfin/internal/core"
	"sailfin/internal/protocol"
)

const openRestyHTTPDir = "/usr/local/openresty/nginx/conf/http.d"
const openRestyVHosts = "/usr/local/openresty/nginx/conf/conf.d"
const openRestyMap = `map $upstream_http_content_type $sailfin_html_head {
    ~*^text/html '<script type="module" src="/__sailfin/boot.js"></script></head>';
    default '</head>';
}
`
const openRestySDK = "sub_filter '</head>' $sailfin_html_head;"

var containerName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

type openRestyNginx struct {
	docker      localDockerHost
	name, relay string
	roots       []string
	ready       *atomic.Bool
	mu          sync.Mutex
	pending     *openRestyPlan
}

type openRestyPlan struct {
	id, container string
	expires       time.Time
	preview       core.NginxPreview
	common        deploymentFile
	sites         map[string]openRestySite
}

type openRestySite struct {
	virtualHost, rootPath, relayPath string
	root, route                      deploymentFile
}

func hostNginxIntegration(ctx context.Context, options serveOptions, auto *nginxAuto) (core.NginxIntegration, error) {
	if options.openRestyContainer == "" {
		return auto, nil
	}
	if !containerName.MatchString(options.openRestyContainer) {
		return nil, errors.New("OpenResty 容器名称无效")
	}
	docker, err := localDocker(ctx)
	if err != nil {
		return nil, err
	}
	return &openRestyNginx{
		docker: docker, name: options.openRestyContainer, relay: auto.relay,
		roots: strings.Split(options.logRoots, ","), ready: &auto.ready,
	}, nil
}

func (p *openRestyNginx) containerMounts(ctx context.Context) (string, map[string]string, error) {
	format := "{{.Id}}\n{{.HostConfig.NetworkMode}}\n{{.State.Running}}\n{{json .Mounts}}"
	output, err := p.docker.output(ctx, "container", "inspect", "--format", format, p.name)
	if err != nil {
		return "", nil, err
	}
	fields := strings.SplitN(output, "\n", 4)
	if len(fields) != 4 || !dockerDigest.MatchString(fields[0]) || fields[1] != "host" || fields[2] != "true" {
		return "", nil, errors.New("请选择运行中、使用宿主机网络的 OpenResty 容器")
	}
	var mounts []struct{ Type, Source, Destination string }
	if err := json.Unmarshal([]byte(fields[3]), &mounts); err != nil {
		return "", nil, err
	}
	paths := map[string]string{}
	for _, mount := range mounts {
		if mount.Type != "bind" || !filepath.IsAbs(mount.Source) {
			continue
		}
		switch strings.TrimRight(mount.Destination, "/") {
		case "/www", openRestyHTTPDir, openRestyVHosts:
			real, err := filepath.EvalSymlinks(mount.Source)
			if err != nil {
				return "", nil, err
			}
			paths[strings.TrimRight(mount.Destination, "/")] = real
		}
	}
	if len(paths) != 3 {
		return "", nil, errors.New("此 OpenResty 实例缺少 1Panel 的站点或配置目录挂载")
	}
	return fields[0], paths, nil
}

func (p *openRestyNginx) Scan(ctx context.Context) (core.NginxPreview, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pending = nil
	container, mounts, err := p.containerMounts(ctx)
	if err != nil {
		return core.NginxPreview{}, err
	}
	common, err := readDeploymentFile(filepath.Join(mounts[openRestyHTTPDir], "sailfin-browser.conf"), true)
	if err != nil {
		return core.NginxPreview{}, err
	}
	if !within(common.path, mounts[openRestyHTTPDir]) || (common.exists && strings.TrimSpace(string(common.before)) != strings.TrimSpace(openRestyMap)) {
		return core.NginxPreview{}, errors.New("已有不同的 Sailfin 公共注入配置")
	}
	common.after = []byte(openRestyMap)
	if !common.exists {
		common.mode = 0644
	}
	entries, err := os.ReadDir(mounts[openRestyVHosts])
	if err != nil {
		return core.NginxPreview{}, err
	}
	id := core.Random()
	plan := &openRestyPlan{id: id, container: container, expires: time.Now().Add(10 * time.Minute),
		preview: core.NginxPreview{ID: id, Sites: []core.NginxSiteResult{}}, common: common, sites: map[string]openRestySite{}}
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".conf") || len(plan.preview.Sites) >= 500 {
			continue
		}
		row, site := p.readSite(entry.Name(), mounts)
		if row.Status == "ready" {
			if seen[row.SiteID] {
				delete(plan.sites, row.SiteID)
				row.Status, row.Reason = "skipped", "站点在多个配置中定义"
			} else {
				plan.sites[row.SiteID] = site
			}
			seen[row.SiteID] = true
		}
		plan.preview.Sites = append(plan.preview.Sites, row)
	}
	for i, row := range plan.preview.Sites {
		if row.Status == "ready" {
			if _, ok := plan.sites[row.SiteID]; ok {
				plan.preview.Ready++
			} else {
				plan.preview.Sites[i].Status, plan.preview.Sites[i].Reason = "skipped", "站点在多个配置中定义"
			}
		}
	}
	p.pending = plan
	return plan.preview, nil
}

func (p *openRestyNginx) readSite(name string, mounts map[string]string) (core.NginxSiteResult, openRestySite) {
	path := filepath.Join(mounts[openRestyVHosts], name)
	row := core.NginxSiteResult{Site: name, File: path, Status: "skipped", OpenResty: true}
	var site openRestySite
	fail := func(message string) (core.NginxSiteResult, openRestySite) {
		row.Reason = message
		return row, site
	}
	file, err := readDeploymentFile(path, false)
	if err != nil || !within(file.path, mounts[openRestyVHosts]) {
		return fail("无法读取已授权的站点配置")
	}
	nodes, err := parseNginx(string(file.before))
	servers := nginxDirective(nodes, "server")
	if err != nil || len(servers) != 1 {
		return fail("需要一个明确的站点 server 块")
	}
	server := servers[0]
	_, origin := nginxSiteOrigin(server)
	domain := strings.TrimPrefix(origin, "https://")
	if !strings.HasPrefix(origin, "https://") || !protocol.ValidSiteID(domain) {
		return fail("需要一个明确的 HTTPS 域名")
	}
	row.Site, row.SiteID, row.Origin = domain, domain, origin
	var proxy string
	for _, directive := range nginxDirective(server.children, "include") {
		if len(directive.args) == 2 && strings.HasPrefix(directive.args[1], "/www/sites/") && strings.HasSuffix(directive.args[1], "/proxy/*.conf") {
			if proxy != "" {
				return fail("站点包含多个代理目录")
			}
			proxy = strings.TrimSuffix(directive.args[1], "/*.conf")
		}
	}
	if proxy == "" || filepath.Clean(proxy) != proxy {
		return fail("需要 1Panel 的反向代理站点配置")
	}
	www := mounts["/www"]
	proxyHost := filepath.Join(www, strings.TrimPrefix(proxy, "/www/"))
	site.virtualHost, site.rootPath, site.relayPath = openRestyVHosts+"/"+name, proxy+"/root.conf", proxy+"/sailfin.conf"
	site.root, err = readDeploymentFile(filepath.Join(proxyHost, "root.conf"), false)
	if err != nil || !within(site.root.path, www) {
		return fail("无法读取已授权的入口代理配置")
	}
	site.route, err = readDeploymentFile(filepath.Join(proxyHost, "sailfin.conf"), true)
	if err != nil || !within(site.route.path, www) {
		return fail("无法读取已授权的转发配置")
	}
	site.route.after = []byte(fmt.Sprintf(`location ^~ /__sailfin/ {
    access_log off;
    client_max_body_size 64k;
    proxy_pass http://%s;
    proxy_set_header X-Sailfin-Site %s;
    proxy_set_header X-Sailfin-Auto "";
    proxy_set_header Authorization "";
    proxy_set_header Cookie "";
    proxy_cache off;
    proxy_read_timeout 10s;
}
`, p.relay, domain))
	if !site.route.exists {
		site.route.mode = 0644
	}
	if site.route.exists && !bytes.Equal(bytes.TrimSpace(site.route.before), bytes.TrimSpace(site.route.after)) {
		return fail("已有不同的 Sailfin 转发配置")
	}
	site.root.after, err = openRestyInject(site.root.before)
	if err != nil {
		return fail(err.Error())
	}
	for _, log := range nginxDirective(server.children, "access_log") {
		if len(log.args) < 2 || !strings.HasPrefix(log.args[1], "/www/sites/") {
			continue
		}
		real, err := filepath.EvalSymlinks(filepath.Join(www, strings.TrimPrefix(log.args[1], "/www/")))
		if err != nil || !within(real, www) {
			continue
		}
		for _, root := range p.roots {
			if root = strings.TrimSpace(root); root == "" {
				continue
			}
			allowed, err := filepath.EvalSymlinks(root)
			if err == nil && within(real, allowed) {
				row.AccessLog = real
				break
			}
		}
		break
	}
	row.Status = "ready"
	if site.route.exists && bytes.Equal(site.root.before, site.root.after) {
		row.Status = "configured"
	}
	return row, site
}

func openRestyInject(raw []byte) ([]byte, error) {
	nodes, err := parseNginx(string(raw))
	locations := nginxDirective(nodes, "location")
	if err != nil || len(locations) != 1 || locations[0].close == 0 {
		return nil, errors.New("入口代理配置需要一个明确的 location 块")
	}
	loc := locations[0]
	if !((len(loc.args) == 2 && loc.args[1] == "/") ||
		(len(loc.args) == 3 && loc.args[1] == "^~" && loc.args[2] == "/")) ||
		len(nginxDirective(loc.children, "proxy_pass")) != 1 ||
		len(nginxDirective(loc.children, "include")) != 0 {
		return nil, errors.New("入口代理使用了不支持的规则")
	}
	for _, filter := range nginxDirective(loc.children, "sub_filter") {
		if len(filter.args) == 3 && strings.EqualFold(filter.args[1], "</head>") {
			if filter.args[2] == "$sailfin_html_head" {
				return raw, nil
			}
			return nil, errors.New("已有不同的页面头部注入规则")
		}
	}
	snippet := "\n    " + openRestySDK + "\n"
	headers := nginxDirective(loc.children, "proxy_set_header")
	hasEncoding := false
	for _, header := range headers {
		if len(header.args) == 3 && strings.EqualFold(header.args[1], "Accept-Encoding") {
			if header.args[2] != "" {
				return nil, errors.New("现有代理压缩规则需手动核对")
			}
			hasEncoding = true
		}
	}
	if !hasEncoding {
		if len(headers) == 0 {
			return nil, errors.New("代理头继承配置需手动核对")
		}
		snippet += "    proxy_set_header Accept-Encoding \"\";\n"
	}
	return []byte(string(raw[:loc.close]) + snippet + string(raw[loc.close:])), nil
}

func within(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func (p *openRestyNginx) Apply(ctx context.Context, id string, ids []string) (core.NginxPreview, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	plan := p.pending
	if plan == nil || plan.id != id || time.Now().After(plan.expires) {
		return core.NginxPreview{}, errors.New("预览已失效，请重新扫描")
	}
	if !p.ready.Load() {
		return core.NginxPreview{}, errors.New("探针转发端口未就绪")
	}
	selected := map[string]bool{}
	files := []*deploymentFile{&plan.common}
	var loaded []string
	for _, id := range ids {
		site, ok := plan.sites[id]
		if !ok || selected[id] {
			return core.NginxPreview{}, errors.New("所选站点不在预览中")
		}
		selected[id] = true
		files = append(files, &site.root, &site.route)
		loaded = append(loaded, site.virtualHost, site.rootPath)
	}
	if len(selected) == 0 {
		return core.NginxPreview{}, errors.New("请选择可接入的站点")
	}
	command := []string{"exec", plan.container, "/usr/local/openresty/bin/openresty"}
	check := func(paths []string) error {
		output, err := p.docker.output(ctx, append(append([]string{}, command...), "-T")...)
		if err != nil {
			return err
		}
		for _, name := range paths {
			if !strings.Contains("\n"+output+"\n", "\n# configuration file "+name+":\n") {
				return fmt.Errorf("OpenResty 未加载 %s", name)
			}
		}
		return nil
	}
	if err := check(loaded); err != nil {
		return core.NginxPreview{}, err
	}
	p.pending = nil

	var written []*deploymentFile
	rollback := func(cause error) (core.NginxPreview, error) {
		for i := len(written) - 1; i >= 0; i-- {
			if err := written[i].restore(); err != nil {
				cause = errors.Join(cause, fmt.Errorf("恢复 %s：%w", written[i].path, err))
			}
		}
		return core.NginxPreview{}, cause
	}
	for _, file := range files {
		if bytes.Equal(file.before, file.after) {
			continue
		}
		if err := file.apply(); err != nil {
			return rollback(err)
		}
		written = append(written, file)
	}
	loaded = append(loaded, openRestyHTTPDir+"/sailfin-browser.conf")
	for id := range selected {
		loaded = append(loaded, plan.sites[id].relayPath)
	}
	if err := check(loaded); err != nil {
		return rollback(err)
	}
	if _, err := p.docker.output(ctx, append(append([]string{}, command...), "-s", "reload")...); err != nil {
		return rollback(err)
	}
	plan.preview.Ready = 0
	for i, row := range plan.preview.Sites {
		if selected[row.SiteID] && row.Status == "ready" {
			row.Status, row.Reason = "applied", "配置校验通过，已请求重载"
		} else if row.Status == "ready" {
			row.Status, row.Reason = "skipped", "本次未选择，可重新扫描后接入"
		}
		plan.preview.Sites[i] = row
	}
	return plan.preview, nil
}
