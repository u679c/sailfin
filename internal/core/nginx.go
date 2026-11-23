package core

import (
	"context"
	"database/sql"
	"errors"
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
	NeedsSDK  bool   `json:"needsSdk,omitempty"`
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

type nginxSDKPlan struct {
	id    string
	sites map[string]NginxSiteResult
}

func (s *Server) scanNginx(w http.ResponseWriter, r *http.Request) error {
	s.siteMu.Lock()
	defer s.siteMu.Unlock()
	s.nginxSDK = nginxSDKPlan{}
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
	sdk := nginxSDKPlan{id: result.ID, sites: make(map[string]NginxSiteResult)}
	for i, row := range result.Sites {
		if row.SiteID == "" || (row.Status != "ready" && row.Status != "configured") {
			continue
		}
		if row.Added {
			if row.OpenResty {
				eligible, err := s.canAddOpenRestySDK(r.Context(), row)
				if err != nil {
					return err
				}
				if eligible {
					sdk.sites[row.SiteID] = row
					result.Sites[i].Status = "ready"
					result.Sites[i].NeedsSDK = true
					result.Ready++
					continue
				}
			}
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
	s.nginxSDK = sdk
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
	upgrades := make(map[string]NginxSiteResult)
	for _, id := range request.SiteIDs {
		if !protocol.ValidSiteID(id) {
			return fail(400, "无效的站点标识")
		}
		if row, ok := s.nginxSDK.sites[id]; ok && s.nginxSDK.id == request.ID {
			eligible, err := s.canAddOpenRestySDK(r.Context(), row)
			if err != nil {
				return err
			}
			if !eligible {
				return fail(409, "站点配置已变化，请重新扫描")
			}
			upgrades[id] = row
			continue
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
	s.nginxSDK = nginxSDKPlan{}
	tx, err := s.DB.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, row := range result.Sites {
		if row.Status == "applied" {
			if existing, ok := upgrades[row.SiteID]; ok {
				if _, err := tx.ExecContext(r.Context(), `UPDATE site_registry SET browser_origin=?, source='openresty'
					WHERE project_id=?`, existing.Origin, protocol.SiteProjectID(row.SiteID)); err != nil {
					return err
				}
				result.Sites[i].NeedsSDK = true
			} else if err := storeSite(r.Context(), tx, nginxSite(row), nginxSource(row), true); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.audit("nginx.enable", "")
	jsonOut(w, http.StatusOK, result)
	return nil
}

func (s *Server) canAddOpenRestySDK(ctx context.Context, row NginxSiteResult) (bool, error) {
	var currentOrigin, accessLog string
	var originInUse bool
	id := protocol.SiteProjectID(row.SiteID)
	err := s.DB.QueryRowContext(ctx, `SELECT s.browser_origin,s.access_log,
		EXISTS(SELECT 1 FROM site_registry WHERE project_id<>s.project_id
			AND source<>'scan' AND browser_origin=?)
		FROM site_registry s WHERE s.project_id=? AND s.source<>'scan'`,
		row.Origin, id).Scan(&currentOrigin, &accessLog, &originInUse)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return row.Origin != "" && accessLog != "" && !originInUse &&
		(currentOrigin == "" || (currentOrigin == row.Origin && row.Status == "ready")), err
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
