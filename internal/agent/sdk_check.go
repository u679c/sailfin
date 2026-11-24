package agent

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"

	"sailfin/internal/branding"
	"sailfin/internal/protocol"
)

const (
	sdkCheckUserAgent = "Sailfin-SDK-Check"
	sdkCheckInterval  = time.Minute
	sdkCheckBatchSize = 5
	sdkHTMLLimit      = 2 << 20
	sdkHTMLAttrs      = `(?:[^"'<>]|"[^"]*"|'[^']*')*`
)

var (
	sdkPageTags = regexp.MustCompile(`(?is)<!--.*?(?:-->|$)|` +
		`<(?:textarea|style|title|noscript|template|xmp|iframe)\b` + sdkHTMLAttrs + `>.*?(?:</(?:textarea|style|title|noscript|template|xmp|iframe)\s*>|$)|` +
		`<script(?:\s+(` + sdkHTMLAttrs + `))?>.*?</script\s*>|` +
		`</?[a-z][a-z0-9:-]*(?:\s+` + sdkHTMLAttrs + `)?/?>`)
	sdkTagAttrs  = regexp.MustCompile(`([^\s"'<>/=]+)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>]+)))?`)
	metadataIPv4 = netip.MustParseAddr("100.100.100.200")
	metadataIPv6 = netip.MustParseAddr("fd00:ec2::254")
)

func (a *Agent) runSiteSDKChecks(ctx context.Context) {
	transport := &http.Transport{
		DialContext:            dialSDKSite,
		MaxIdleConns:           sdkCheckBatchSize,
		IdleConnTimeout:        15 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 3 || !sameSDKOrigin(req.URL, via[0].URL) {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	results := make(map[string]protocol.SiteSDKCheck)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	dirty := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		sites := a.relaySites.Load().sites
		for id, check := range results {
			if sites[id].BrowserOrigin != check.Origin {
				delete(results, id)
				dirty = true
			}
		}
		batch := make([]Site, 0, sdkCheckBatchSize)
		staleBefore := time.Now().Add(-sdkCheckInterval).UnixMilli()
		for _, site := range sites {
			if site.BrowserOrigin != "" && results[site.ID].CheckedAt < staleBefore {
				batch = append(batch, site)
				if len(batch) == sdkCheckBatchSize {
					break
				}
			}
		}
		type result struct {
			id string
			protocol.SiteSDKCheck
		}
		done := make(chan result, len(batch))
		for _, site := range batch {
			go func(site Site) {
				status, detail := checkSiteSDK(ctx, client, site.BrowserOrigin)
				done <- result{site.ID, protocol.SiteSDKCheck{
					Origin: site.BrowserOrigin, Status: status, Detail: detail, CheckedAt: time.Now().UnixMilli(),
				}}
			}(site)
		}
		for range batch {
			check := <-done
			results[check.id] = check.SiteSDKCheck
			dirty = true
		}
		if dirty && ctx.Err() == nil {
			if err := a.request(ctx, http.MethodPost, "/api/v1/agent/site-sdk-status", results, nil); err != nil {
				slog.Warn("could not report site SDK status", "error", err)
			} else {
				dirty = false
			}
		}
	}
}

func checkSiteSDK(parent context.Context, client *http.Client, origin string) (status, detail string) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	page, err := getSDKResource(ctx, client, origin+"/")
	if err != nil {
		return "check_failed", "首页检查失败：" + err.Error()
	}
	defer page.Body.Close()
	contentType, _, _ := mime.ParseMediaType(page.Header.Get("Content-Type"))
	if contentType != "text/html" && contentType != "application/xhtml+xml" {
		return "check_failed", "首页未返回 HTML"
	}
	body, err := io.ReadAll(io.LimitReader(page.Body, sdkHTMLLimit+1))
	if err != nil {
		return "check_failed", "无法读取首页响应"
	}
	src := sdkScriptURL(body, page.Request.URL)
	if src == nil {
		if len(body) > sdkHTMLLimit {
			return "check_failed", "首页响应超过检查大小限制"
		}
		return "not_injected", "首页未引用帆鳍 SDK 脚本"
	}

	script, err := getSDKResource(ctx, client, src.String())
	if err != nil {
		return "script_unavailable", "脚本检查失败：" + err.Error()
	}
	defer script.Body.Close()
	contentType, _, _ = mime.ParseMediaType(script.Header.Get("Content-Type"))
	if contentType != "text/javascript" && contentType != "application/javascript" {
		return "script_unavailable", "脚本未返回 JavaScript"
	}
	if _, err := io.CopyN(io.Discard, script.Body, 1); err != nil {
		return "script_unavailable", "无法读取脚本响应"
	}
	return "injected", "首页已注入脚本，脚本可访问"
}

func getSDKResource(ctx context.Context, client *http.Client, address string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, errors.New("地址无效")
	}
	req.Header.Set("User-Agent", sdkCheckUserAgent)
	res, err := client.Do(req)
	if err != nil {
		return nil, errors.New("请求失败或超时")
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return res, nil
}

func sdkScriptURL(body []byte, page *url.URL) *url.URL {
	for _, tag := range sdkPageTags.FindAllSubmatch(body, -1) {
		if len(tag[1]) == 0 {
			continue
		}
		attrs := make(map[string]string)
		for _, attr := range sdkTagAttrs.FindAllStringSubmatch(string(tag[1]), -1) {
			name := strings.ToLower(attr[1])
			if _, exists := attrs[name]; !exists {
				attrs[name] = html.UnescapeString(attr[2] + attr[3] + attr[4])
			}
		}
		if !strings.EqualFold(strings.TrimSpace(attrs["type"]), "module") {
			continue
		}
		src, err := page.Parse(strings.TrimSpace(attrs["src"]))
		if err != nil || !sameSDKOrigin(src, page) {
			continue
		}
		switch src.Path {
		case branding.BrowserBootPath, branding.PreviousBrowserBootPath, branding.LegacyRelayPrefix + "boot.js":
			return src
		}
	}
	return nil
}

func sameSDKOrigin(a, b *url.URL) bool {
	return a.User == nil && a.Scheme == b.Scheme &&
		strings.EqualFold(a.Hostname(), b.Hostname()) && sdkPort(a) == sdkPort(b)
}

func sdkPort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

func dialSDKSite(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	dialer := net.Dialer{Timeout: 2 * time.Second}
	dialErr := errors.New("SDK check has no allowed destination")
	for _, ip := range ips {
		ip = ip.Unmap()
		if (!ip.IsGlobalUnicast() && !ip.IsLoopback()) || ip == metadataIPv4 || ip == metadataIPv6 {
			continue
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		dialErr = err
	}
	return nil, dialErr
}
