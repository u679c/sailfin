package core

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"sailfin/internal/protocol"
)

type NginxSiteResult struct {
	Site      string `json:"site"`
	File      string `json:"file,omitempty"`
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty"`
	SiteID    string `json:"siteId,omitempty"`
	Origin    string `json:"origin,omitempty"`
	AccessLog string `json:"accessLog,omitempty"`
	OpenResty bool   `json:"openResty,omitempty"`
	Added     bool   `json:"added,omitempty"`
}

type NginxPreview struct {
	ID    string            `json:"id"`
	Ready int               `json:"ready"`
	Sites []NginxSiteResult `json:"sites"`
}

type NginxIntegration interface {
	Scan(context.Context) (NginxPreview, error)
	Apply(context.Context, string, []string) (NginxPreview, error)
}

func (s *Server) scanNginx(w http.ResponseWriter, r *http.Request) error {
	s.siteMu.Lock()
	defer s.siteMu.Unlock()
	result, err := s.cfg.Nginx.Scan(r.Context())
	if err != nil {
		return fail(409, err.Error())
	}
	added, err := s.addedSiteKeys(r.Context())
	if err != nil {
		return err
	}
	result.Sites = uniqueScanSites(result.Sites, added)
	result.Ready = 0
	for i, row := range result.Sites {
		if row.SiteID == "" || (row.Status != "ready" && row.Status != "configured") {
			continue
		}
		if row.Added {
			result.Sites[i].Status, result.Sites[i].Reason = "configured", ""
			continue
		}
		source := "scan"
		if row.Status == "configured" {
			source = nginxSource(row)
		}
		if err := s.storeSites(r.Context(), []protocol.Site{nginxSite(row)}, source, false); err != nil {
			result.Sites[i].Status, result.Sites[i].Reason = "skipped", err.Error()
		} else if row.Status == "configured" {
			result.Sites[i].Added = true
		} else {
			result.Ready++
		}
	}
	jsonOut(w, http.StatusOK, result)
	return nil
}

func (s *Server) applyNginx(w http.ResponseWriter, r *http.Request) error {
	s.siteMu.Lock()
	defer s.siteMu.Unlock()
	var request struct {
		ID      string   `json:"id"`
		SiteIDs []string `json:"siteIds"`
	}
	if err := decode(r, &request); err != nil {
		return err
	}
	if len(request.SiteIDs) == 0 || len(request.SiteIDs) > 50 {
		return fail(400, "请选择 1 到 50 个站点")
	}
	var configured int
	if err := s.DB.QueryRowContext(r.Context(), "SELECT count(*) FROM site_registry WHERE site_id<>'' AND source<>'scan'").Scan(&configured); err != nil {
		return err
	}
	added, err := s.addedSiteKeys(r.Context())
	if err != nil {
		return err
	}
	for _, id := range request.SiteIDs {
		if !protocol.ValidSiteID(id) {
			return fail(400, "无效的站点标识")
		}
		row := NginxSiteResult{SiteID: id}
		err := s.DB.QueryRowContext(r.Context(), `SELECT browser_origin,access_log FROM site_registry
			WHERE project_id=? AND source='scan'`, protocol.SiteProjectID(id)).Scan(&row.Origin, &row.AccessLog)
		if err != nil || isAddedSite(row, added) {
			return fail(409, "站点已添加或配置已变化，请重新扫描")
		}
		configured++
	}
	if configured > 50 {
		return fail(409, "启用后将超过 50 个已配置站点")
	}
	result, err := s.cfg.Nginx.Apply(r.Context(), request.ID, request.SiteIDs)
	if err != nil {
		return fail(409, err.Error())
	}
	var applied []protocol.Site
	source := "nginx"
	for _, row := range result.Sites {
		if row.Status == "applied" {
			source = nginxSource(row)
			applied = append(applied, nginxSite(row))
		}
	}
	if err := s.storeSites(r.Context(), applied, source, true); err != nil {
		return err
	}
	s.audit("nginx.enable", "")
	jsonOut(w, http.StatusOK, result)
	return nil
}

func siteOriginKey(origin string) string {
	u, err := url.Parse(origin)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	host, port := strings.ToLower(u.Hostname()), u.Port()
	if port != "" && !(u.Scheme == "http" && port == "80") && !(u.Scheme == "https" && port == "443") {
		host = net.JoinHostPort(host, port)
	}
	return "host:" + host
}

func (s *Server) addedSiteKeys(ctx context.Context) (map[string]bool, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT project_id,site_id,browser_origin,access_log FROM site_registry WHERE source<>'scan'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := map[string]bool{}
	for rows.Next() {
		var project, id, origin, path string
		if err := rows.Scan(&project, &id, &origin, &path); err != nil {
			return nil, err
		}
		keys[project] = true
		if host := siteOriginKey(origin); host != "" {
			keys[host] = true
		} else if strings.Contains(id, ".") {
			keys["host:"+strings.ToLower(id)] = true
		}
		if path != "" {
			keys["log:"+filepath.Clean(path)] = true
		}
	}
	return keys, rows.Err()
}

func isAddedSite(row NginxSiteResult, added map[string]bool) bool {
	return (row.SiteID != "" && added[protocol.SiteProjectID(row.SiteID)]) ||
		added[siteOriginKey(row.Origin)] ||
		(row.AccessLog != "" && added["log:"+filepath.Clean(row.AccessLog)])
}

func uniqueScanSites(rows []NginxSiteResult, added map[string]bool) []NginxSiteResult {
	result := make([]NginxSiteResult, 0, len(rows))
	seen := map[string]int{}
	rank := func(row NginxSiteResult) int {
		switch {
		case row.Added || row.Status == "configured":
			return 3
		case row.Status == "ready":
			return 2
		default:
			return 1
		}
	}
	for _, row := range rows {
		row.Added = isAddedSite(row, added)
		key := siteOriginKey(row.Origin)
		if key == "" && row.SiteID != "" {
			key = protocol.SiteProjectID(row.SiteID)
		}
		if key != "" {
			if index, exists := seen[key]; exists {
				if rank(row) > rank(result[index]) {
					result[index] = row
				}
				continue
			}
			seen[key] = len(result)
		}
		result = append(result, row)
	}
	return result
}

func nginxSite(row NginxSiteResult) protocol.Site {
	consent := true
	if origin, err := url.Parse(row.Origin); err == nil {
		host := origin.Hostname()
		consent = host != "localhost" && !net.ParseIP(host).IsLoopback()
	}
	return protocol.Site{ID: row.SiteID, Name: row.Site, BrowserOrigin: row.Origin, AccessLog: row.AccessLog, RequireConsent: consent}
}

func nginxSource(row NginxSiteResult) string {
	if row.OpenResty {
		return "openresty"
	}
	return "nginx"
}
