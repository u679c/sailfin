package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"sailfin/internal/agent"
	"sailfin/internal/protocol"
)

func (s *Server) collectedSites(w http.ResponseWriter, r *http.Request) error {
	if s.cfg.LocalOnly {
		return s.query(w, r, `SELECT p.id,p.name,p.created_at,
			(SELECT max(e.received_at) FROM events e WHERE e.project_id=p.id) AS last_event
			FROM site_registry a JOIN projects p ON p.id=a.project_id
			WHERE a.enabled=1 ORDER BY last_event DESC,p.created_at DESC LIMIT 500`)
	}
	return s.query(w, r, `SELECT p.id,p.name,p.created_at,
		(SELECT max(e.received_at) FROM events e WHERE e.project_id=p.id) AS last_event
		FROM projects p
		WHERE p.id GLOB 'site_*' OR EXISTS(SELECT 1 FROM events e WHERE e.project_id=p.id)
		ORDER BY last_event DESC,p.created_at DESC LIMIT 500`)
}

type siteElementConfig struct {
	ClickSelectors    []string          `json:"clickSelectors"`
	ClickDescriptions map[string]string `json:"clickDescriptions"`
}

func (s *Server) siteElements(w http.ResponseWriter, r *http.Request) error {
	var selectors, descriptions []byte
	var browserConfigured bool
	err := s.DB.QueryRowContext(r.Context(), `SELECT
		COALESCE(json_extract(p.collection,'$.clickSelectors'),'[]'),
		COALESCE(json_extract(p.collection,'$.clickDescriptions'),'{}'),
		CASE WHEN ? THEN COALESCE(a.browser_origin,'')<>'' ELSE p.browser_enabled END
		FROM projects p LEFT JOIN site_registry a ON a.project_id=p.id
		WHERE p.id=? AND (?=0 OR a.project_id IS NOT NULL)`,
		s.cfg.LocalOnly, r.PathValue("id"), s.cfg.LocalOnly).Scan(&selectors, &descriptions, &browserConfigured)
	if errors.Is(err, sql.ErrNoRows) {
		return fail(404, "站点不存在")
	}
	if err != nil {
		return err
	}
	jsonOut(w, 200, map[string]any{
		"clickSelectors": json.RawMessage(selectors), "clickDescriptions": json.RawMessage(descriptions),
		"browserConfigured": browserConfigured,
	})
	return nil
}

func (s *Server) saveSiteElements(w http.ResponseWriter, r *http.Request) error {
	var settings struct {
		siteElementConfig
		Previous *siteElementConfig `json:"previous"`
	}
	if err := decode(r, &settings); err != nil {
		return err
	}
	if settings.ClickSelectors == nil || len(settings.ClickSelectors) > 50 {
		return fail(400, "请提供点击选择器列表，最多 50 条")
	}
	for i, selector := range settings.ClickSelectors {
		selector = strings.TrimSpace(selector)
		if !validClickSelector(selector) {
			return fail(400, fmt.Sprintf("第 %d 条选择器无效，必须为单行且不超过 200 字节", i+1))
		}
		settings.ClickSelectors[i] = selector
	}
	tx, err := s.DB.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	id := r.PathValue("id")
	var collection string
	err = tx.QueryRowContext(r.Context(), `SELECT collection FROM projects WHERE id=?
		AND (?=0 OR EXISTS(SELECT 1 FROM site_registry WHERE project_id=projects.id))`,
		id, s.cfg.LocalOnly).Scan(&collection)
	if errors.Is(err, sql.ErrNoRows) {
		return fail(404, "站点不存在")
	}
	if err != nil {
		return err
	}
	var current siteElementConfig
	if err := json.Unmarshal([]byte(collection), &current); err != nil {
		return err
	}
	if previous := settings.Previous; previous != nil &&
		(!slices.Equal(previous.ClickSelectors, current.ClickSelectors) || !maps.Equal(previous.ClickDescriptions, current.ClickDescriptions)) {
		return fail(409, "元素规则已更新，请关闭抽屉后重新编辑")
	}
	descriptions := settings.ClickDescriptions
	if descriptions == nil {
		descriptions = current.ClickDescriptions
	}
	settings.ClickDescriptions = make(map[string]string)
	for _, selector := range settings.ClickSelectors {
		if description := strings.TrimSpace(descriptions[selector]); description != "" {
			settings.ClickDescriptions[selector] = description
		}
	}
	selectorsJSON, err := json.Marshal(settings.ClickSelectors)
	if err != nil {
		return err
	}
	descriptionsJSON, err := json.Marshal(settings.ClickDescriptions)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE projects SET collection=json_set(
		CASE WHEN json_type(collection)='object' THEN collection ELSE '{}' END,
		'$.clickSelectors',json(?),'$.clickDescriptions',json(?)) WHERE id=?`,
		string(selectorsJSON), string(descriptionsJSON), id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.audit("site.elements.save", id)
	jsonOut(w, 200, settings.siteElementConfig)
	return nil
}

func validClickSelector(selector string) bool {
	return strings.TrimSpace(selector) != "" && len(selector) <= 200 && !strings.ContainsAny(selector, "\r\n\x00")
}

func (s *Server) siteEvents(w http.ResponseWriter, r *http.Request) error {
	agentID, project, err := s.agentIdentity(r)
	if err != nil {
		return err
	}
	if project != "" {
		return fail(403, "site collection requires a host enrollment")
	}
	return s.ingestSiteEvents(w, r, agentID)
}

func (s *Server) ingestSiteEvents(w http.ResponseWriter, r *http.Request, agentID string) error {
	var batch protocol.SiteBatch
	if err := decode(r, &batch); err != nil {
		return err
	}
	name := strings.TrimSpace(batch.Name)
	if !protocol.ValidSiteID(batch.SiteID) || name == "" || len(name) > 120 {
		return fail(400, "invalid site identity")
	}
	if batch.Source != "browser" && batch.Source != "host" {
		return fail(400, "unknown collection source")
	}

	// The stable operator-assigned ID merges the two ingestion paths and replicas.
	projectID := protocol.SiteProjectID(batch.SiteID)
	for i := range batch.Events {
		event := &batch.Events[i]
		if batch.Source == "host" {
			if event.EventType != "http_request" || event.SessionID != "" || event.VisitorID != "" {
				return fail(400, "invalid host event")
			}
		} else if !browserTypes[event.EventType] {
			return fail(400, "invalid browser event")
		}
		event.ProjectID = projectID
	}
	return s.ingestBatch(w, r, projectID, batch.Source, agentID, batch.Events, name)
}

func (s *Server) managedSites(w http.ResponseWriter, r *http.Request) error {
	rows, err := s.rows(r.Context(), `SELECT p.id,p.name,a.site_id AS siteId,a.source,
		a.enabled,a.access_log AS accessLog,a.browser_origin AS browserOrigin,
		a.require_consent AS requireConsent,
		(SELECT max(e.received_at) FROM events e WHERE e.project_id=p.id) AS last_event
		FROM site_registry a JOIN projects p ON p.id=a.project_id ORDER BY p.name,p.id`)
	if err != nil {
		return err
	}
	s.mu.Lock()
	checks := s.siteSDK
	s.mu.Unlock()
	staleBefore := time.Now().Add(-3 * time.Minute).UnixMilli()
	for _, row := range rows {
		origin, _ := row["browserOrigin"].(string)
		if origin == "" {
			row["sdkStatus"] = "unconfigured"
			continue
		}
		siteID, _ := row["siteId"].(string)
		check := checks[siteID]
		if check.Origin != origin || check.CheckedAt < staleBefore {
			row["sdkStatus"] = "pending"
			continue
		}
		row["sdkStatus"] = check.Status
		row["sdkDetail"] = check.Detail
		row["sdkCheckedAt"] = check.CheckedAt
	}
	jsonOut(w, 200, rows)
	return nil
}

func (s *Server) saveSite(w http.ResponseWriter, r *http.Request) error {
	s.siteMu.Lock()
	defer s.siteMu.Unlock()
	var site protocol.Site
	if err := decode(r, &site); err != nil {
		return err
	}
	site.Name = strings.TrimSpace(site.Name)
	site.AccessLog = strings.TrimSpace(site.AccessLog)
	site.BrowserOrigin = strings.TrimSpace(site.BrowserOrigin)
	if site.AccessLog != "" && !filepath.IsAbs(site.AccessLog) {
		return fail(400, "请填写日志文件的绝对路径")
	}
	if len(site.AccessLog) > 2048 || len(site.BrowserOrigin) > 512 {
		return fail(400, "站点地址或日志路径过长")
	}
	if site.AccessLog != "" {
		site.AccessLog = filepath.Clean(site.AccessLog)
	}
	if err := agent.ValidateSites([]agent.Site{site}); err != nil {
		return fail(400, err.Error())
	}
	if err := s.storeSites(r.Context(), []protocol.Site{site}, "manual", false); err != nil {
		return err
	}
	s.audit("site.configure", protocol.SiteProjectID(site.ID))
	jsonOut(w, 200, map[string]string{"id": protocol.SiteProjectID(site.ID)})
	return nil
}

func (s *Server) toggleSite(w http.ResponseWriter, r *http.Request) error {
	s.siteMu.Lock()
	defer s.siteMu.Unlock()
	var settings struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decode(r, &settings); err != nil {
		return err
	}
	if settings.Enabled == nil {
		return fail(400, "请选择采集状态")
	}
	id := r.PathValue("id")
	result, err := s.DB.ExecContext(r.Context(), `UPDATE site_registry
		SET enabled_since=CASE WHEN enabled=0 AND ? THEN ? ELSE enabled_since END, enabled=?
		WHERE project_id=? AND (source<>'scan' OR NOT ?)`, *settings.Enabled, Now(), *settings.Enabled, id, *settings.Enabled)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil {
		return err
	} else if count == 0 {
		return fail(409, "请先在扫描结果中接入该站点")
	}
	action := "site.disable"
	if *settings.Enabled {
		action = "site.enable"
	}
	s.audit(action, id)
	jsonOut(w, 200, map[string]bool{"enabled": *settings.Enabled})
	return nil
}

func (s *Server) storeSites(ctx context.Context, sites []protocol.Site, source string, enabled bool) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, site := range sites {
		if err := storeSite(ctx, tx, site, source, enabled); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func storeSite(ctx context.Context, tx *sql.Tx, site protocol.Site, source string, enabled bool) error {
	id := protocol.SiteProjectID(site.ID)
	var conflict int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM site_registry
		WHERE project_id<>? AND source<>'scan' AND ((access_log<>'' AND access_log=?) OR (browser_origin<>'' AND browser_origin=?))`,
		id, site.AccessLog, site.BrowserOrigin).Scan(&conflict); err != nil {
		return err
	}
	if conflict != 0 {
		if source == "file" {
			return nil
		}
		return fail(409, fmt.Sprintf("站点 %s 的地址或日志已经被使用", site.Name))
	}
	var exists, total, paths int
	if err := tx.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM site_registry WHERE project_id=?),
		(SELECT count(*) FROM site_registry),
		(SELECT count(*) FROM site_registry WHERE project_id<>? AND site_id<>'' AND source<>'scan')`,
		id, id).Scan(&exists, &total, &paths); err != nil {
		return err
	}
	if (exists == 0 && total >= 500) || (source != "scan" && paths >= 50) {
		return fail(409, "已达到站点配置上限")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO projects(id,name,description,created_at) VALUES(?,?,'',?)
		ON CONFLICT(id) DO UPDATE SET name=CASE WHEN ?='manual' THEN excluded.name ELSE projects.name END`,
		id, site.Name, Now(), source); err != nil {
		return err
	}
	since := int64(0)
	if (source == "nginx" || source == "openresty") && enabled {
		since = Now()
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO site_registry
		(project_id,site_id,source,enabled,enabled_since,access_log,browser_origin,require_consent)
		VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(project_id) DO UPDATE SET
		site_id=excluded.site_id,
		source=CASE WHEN excluded.source='manual' AND site_registry.source IN ('nginx','openresty') THEN site_registry.source ELSE excluded.source END,
		enabled=CASE WHEN excluded.source IN ('nginx','openresty') AND excluded.enabled THEN 1 ELSE site_registry.enabled END,
		enabled_since=CASE WHEN excluded.enabled AND site_registry.enabled=0 THEN excluded.enabled_since ELSE site_registry.enabled_since END,
		access_log=CASE WHEN excluded.source IN ('scan','nginx','openresty') AND site_registry.access_log<>'' THEN site_registry.access_log ELSE excluded.access_log END,
		browser_origin=excluded.browser_origin,
		require_consent=CASE WHEN excluded.source IN ('nginx','openresty') AND site_registry.source IN ('manual','file','openresty') AND site_registry.browser_origin<>'' THEN site_registry.require_consent ELSE excluded.require_consent END
		WHERE excluded.source='manual' OR site_registry.source='legacy'
		   OR (excluded.source='file' AND site_registry.source='file')
		   OR (excluded.source IN ('nginx','openresty') AND site_registry.source='scan')
		   OR (excluded.source='openresty' AND excluded.enabled)`,
		id, site.ID, source, enabled, since, site.AccessLog, site.BrowserOrigin, site.RequireConsent)
	return err
}

func (s *Server) siteLogPaths(w http.ResponseWriter, _ *http.Request) error {
	s.mu.Lock()
	paths := append([]string{}, s.siteLogs...)
	s.mu.Unlock()
	jsonOut(w, 200, paths)
	return nil
}
