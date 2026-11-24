package main

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	"sailfin/internal/branding"
	"sailfin/internal/core"
	"sailfin/internal/protocol"
)

type nginxInsertion struct {
	offset int
	text   string
}

func planAutomaticInjection(text, path, address, authPath, defaultIP string) (string, []core.NginxSiteResult, error) {
	return planHostInjection(text, path, address, authPath, defaultIP, nil)
}

func planHostInjection(text, path, address, authPath, defaultIP string, selected map[string]bool) (string, []core.NginxSiteResult, error) {
	nodes, err := parseNginx(text)
	if err != nil {
		return "", nil, err
	}
	servers := nginxDirective(nodes, "server")
	for _, http := range nginxDirective(nodes, "http") {
		servers = append(servers, nginxDirective(http.children, "server")...)
	}
	var reports []core.NginxSiteResult
	var changes []nginxInsertion
	for _, server := range servers {
		locations := nginxDirective(server.children, "location")
		if len(locations) == 0 && len(nginxDirective(server.children, "server_name")) == 0 {
			continue
		}
		report := core.NginxSiteResult{Site: nginxSiteLabel(server), File: path, Status: "skipped"}
		report.SiteID, report.Origin = nginxSiteOrigin(server, defaultIP)
		if selected != nil && !selected[report.SiteID] {
			continue
		}
		if report.SiteID == "" {
			report.Reason = "无法确定站点的唯一访问地址，请配置 server_name 或明确的监听 IP"
			reports = append(reports, report)
			continue
		}
		inserts, reason := autoServerChanges(text, server, address, authPath)
		if reason == "" {
			report.Status = "ready"
			changes = append(changes, inserts...)
		} else if reason == "已接入" {
			report.Status = "configured"
		}
		report.Reason = reason
		reports = append(reports, report)
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].offset > changes[j].offset })
	for _, change := range changes {
		text = text[:change.offset] + change.text + text[change.offset:]
	}
	return text, reports, nil
}

func nginxSiteOrigin(server nginxNode, defaultIP string) (string, string) {
	names := nginxDirective(server.children, "server_name")
	var host string
	if len(names) != 0 {
		if len(names) != 1 || len(names[0].args) != 2 {
			return "", ""
		}
		host = strings.ToLower(names[0].args[1])
		if host == "_" {
			host = ""
		}
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		host = ""
		if !ip.IsUnspecified() {
			host = ip.String()
		}
	} else if host != "" && (!protocol.ValidSiteID(host) || strings.Contains(host, "_")) {
		return "", ""
	}
	scheme, port := "http", "80"
	var listenIP string
	ambiguousIP := false
	for _, listen := range nginxDirective(server.children, "listen") {
		if len(listen.args) < 2 {
			continue
		}
		value := listen.args[1]
		boundHost := strings.Trim(value, "[]")
		if address, candidate, err := net.SplitHostPort(value); err == nil {
			boundHost, value = address, candidate
		} else if net.ParseIP(boundHost) != nil {
			value = "80"
		}
		if ip := net.ParseIP(boundHost); ip != nil && !ip.IsUnspecified() {
			if listenIP != "" && listenIP != ip.String() {
				ambiguousIP = true
			}
			listenIP = ip.String()
		}
		number, err := strconv.Atoi(value)
		if err != nil || number < 1 || number > 65535 {
			continue
		}
		if number == 443 || nginxHasArg([]nginxNode{listen}, "listen", "ssl") {
			if scheme != "https" {
				scheme, port = "https", strconv.Itoa(number)
			}
		} else if scheme == "http" {
			port = strconv.Itoa(number)
		}
	}
	if host == "" {
		if ambiguousIP {
			return "", ""
		}
		host = listenIP
		if host == "" {
			host = defaultIP
		}
		if host == "" {
			return "", ""
		}
	}
	authority := host
	if strings.Contains(host, ":") {
		authority = "[" + host + "]"
	}
	if (scheme == "http" && port != "80") || (scheme == "https" && port != "443") {
		host = net.JoinHostPort(host, port)
		authority = host
	}
	return autoNginxSiteID(host), scheme + "://" + authority
}

func nginxSiteLabel(server nginxNode) string {
	var names, listens []string
	for _, node := range nginxDirective(server.children, "server_name") {
		names = append(names, node.args[1:]...)
	}
	for _, node := range nginxDirective(server.children, "listen") {
		if len(node.args) > 1 {
			listens = append(listens, node.args[1])
		}
	}
	name := strings.Join(names, ", ")
	if name == "" || name == "_" {
		name = "默认站点（按访问地址识别）"
	}
	if len(listens) != 0 {
		name += " · " + strings.Join(listens, ", ")
	}
	return name
}

func autoServerChanges(text string, server nginxNode, address, authPath string) ([]nginxInsertion, string) {
	relay := nginxAutoRelay(address, authPath)
	serverText := text[server.start:server.end]
	if hasSailfinRelay(serverText, relay) {
		return nil, "已接入"
	}
	if strings.Contains(serverText, branding.LegacyHTML) {
		return nil, "已接入"
	}
	if strings.Contains(serverText, "/__sailfin/") || strings.Contains(serverText, branding.LegacyRelayPrefix) {
		return nil, "已有帆鳍或同路径规则"
	}
	for _, loc := range nginxDirective(server.children, "location") {
		for _, arg := range loc.args[1:] {
			if arg == "/__sailfin" || strings.HasPrefix(arg, "/__sailfin/") {
				return nil, "已有同名采集路由"
			}
		}
	}
	if len(nginxDirective(server.children, "include")) != 0 ||
		len(nginxDirective(server.children, "sub_filter")) != 0 ||
		len(nginxDirective(server.children, "sub_filter_types")) != 0 {
		return nil, "站点含 include 或自定义响应替换，需手动接入"
	}
	if len(nginxDirective(server.children, "return")) != 0 {
		return nil, "站点入口直接返回或重定向"
	}

	var inserts []nginxInsertion
	// HTTP sub_filter matches text/html by default. Suppress upstream compression
	// only if headers are explicit here, so inherited proxy headers are preserved.
	for _, loc := range nginxDirective(server.children, "location") {
		if len(loc.args) < 2 || strings.HasPrefix(loc.args[1], "@") {
			continue
		}
		if len(nginxDirective(loc.children, "include")) != 0 ||
			len(nginxDirective(loc.children, "return")) != 0 ||
			len(nginxDirective(loc.children, "sub_filter")) != 0 ||
			len(nginxDirective(loc.children, "sub_filter_types")) != 0 ||
			len(nginxDirective(loc.children, "location")) != 0 {
			continue
		}
		isProxy := len(nginxDirective(loc.children, "proxy_pass")) != 0
		isStatic := len(nginxDirective(loc.children, "root")) != 0 ||
			len(nginxDirective(loc.children, "alias")) != 0 ||
			len(nginxDirective(server.children, "root")) != 0 ||
			len(nginxDirective(loc.children, "try_files")) != 0 ||
			len(nginxDirective(loc.children, "index")) != 0
		if !isProxy && !isStatic {
			continue
		}
		snippet := "\n        # Sailfin auto injection\n" +
			"        sub_filter '</head>' '" + sailfinHTML + "';\n"
		if isProxy {
			headers := nginxDirective(loc.children, "proxy_set_header")
			if len(headers) != 0 {
				hasHeader, compressed := false, false
				for _, header := range headers {
					if len(header.args) == 3 && strings.EqualFold(header.args[1], "Accept-Encoding") {
						hasHeader = true
						compressed = compressed || header.args[2] != ""
					}
				}
				if compressed {
					continue
				}
				if !hasHeader {
					snippet += "        proxy_set_header Accept-Encoding \"\";\n"
				}
			}
		}
		inserts = append(inserts, nginxInsertion{loc.close, snippet + "    "})
	}
	if len(inserts) == 0 && len(nginxDirective(server.children, "location")) == 0 &&
		len(nginxDirective(server.children, "root")) != 0 {
		inserts = append(inserts, nginxInsertion{server.close,
			"\n    sub_filter '</head>' '" + sailfinHTML + "';\n" + relay})
		return inserts, ""
	}
	if len(inserts) == 0 {
		return nil, "未找到兼容的 HTML 路由"
	}
	return append(inserts, nginxInsertion{server.close, relay}), ""
}

func nginxAutoRelay(relay, authPath string) string {
	return fmt.Sprintf(`
    # Sailfin automatic collection
    location ^~ /__sailfin/ {
        access_log off;
        client_max_body_size 64k;
        proxy_pass http://%s;
        proxy_set_header X-Sailfin-Site "";
        include "%s";
        proxy_set_header X-Sailfin-Origin "$scheme://$http_host";
        proxy_set_header Authorization "";
        proxy_set_header Cookie "";
        proxy_cache off;
        proxy_read_timeout 10s;
    }
`, relay, strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(authPath))
}
