package core

import (
	"encoding/json"
	"net/http"
)

func clickGroupQuery(filter string) string {
	return `WITH clicked AS (
		SELECT project_id,visitor_id,timestamp,id,
			COALESCE(NULLIF(json_extract(properties,'$.pagePath'),''),path) AS page,
			CASE
				WHEN NULLIF(json_extract(properties,'$.elementId'),'') IS NOT NULL
					THEN json_array('id',json_extract(properties,'$.elementId'))
				WHEN NULLIF(json_extract(properties,'$.selector'),'') IS NOT NULL
					THEN json_array('selector',json_extract(properties,'$.selector'))
				ELSE json_array('legacy',json_extract(properties,'$.element'),json_extract(properties,'$.label'),json_extract(properties,'$.region'))
			END AS element_key,
			json_extract(properties,'$.label') AS label,
			json_extract(properties,'$.region') AS region,
			json_extract(properties,'$.elementId') AS element_id,
			json_extract(properties,'$.selector') AS selector,
			json_extract(properties,'$.ruleSelector') AS rule_selector
		FROM events WHERE type='click' AND source_type='browser' AND ` + filter + `
	), click_groups AS (
		SELECT c.project_id,c.page,c.id,c.visitor_id,c.timestamp,c.label,c.region,c.element_id,c.selector,c.rule_selector,
			COALESCE(m.group_key,c.element_key) AS element_key
		FROM clicked c LEFT JOIN click_merge_members m
			ON m.project_id=c.project_id AND m.page=c.page AND m.element_key=c.element_key
	)`
}

func (s *Server) clickStats(w http.ResponseWriter, r *http.Request, filter string, args []any) error {
	return s.pagedQuery(w, r, clickGroupQuery(filter)+`, ranked AS (
		SELECT *,row_number() OVER (
			PARTITION BY project_id,page,element_key ORDER BY timestamp DESC,id DESC
		) AS position FROM click_groups
	)
	SELECT r.project_id,p.name AS site_name,r.page AS path,r.element_key,
		max(CASE WHEN position=1 THEN label END) AS label,
		max(CASE WHEN position=1 THEN region END) AS region,
		max(CASE WHEN position=1 THEN element_id END) AS element_id,
		max(CASE WHEN position=1 THEN selector END) AS selector,
		max(CASE WHEN position=1 THEN (
			SELECT d.value FROM json_each(p.collection,'$.clickDescriptions') d WHERE d.key=r.rule_selector
		) END) AS description,
		count(*) AS clicks,count(DISTINCT NULLIF(visitor_id,'')) AS visitors,
		max(timestamp) AS last_click
	FROM ranked r JOIN projects p ON p.id=r.project_id
	GROUP BY r.project_id,r.page,r.element_key
	ORDER BY clicks DESC,last_click DESC,r.project_id,r.page,r.element_key
	LIMIT ? OFFSET ?`, args...)
}

func (s *Server) mergeClicks(w http.ResponseWriter, r *http.Request) error {
	var request struct {
		ProjectID   string   `json:"projectId"`
		Page        string   `json:"page"`
		ElementKeys []string `json:"elementKeys"`
	}
	if err := decode(r, &request); err != nil {
		return err
	}
	keys := make(map[string]struct{}, len(request.ElementKeys))
	for _, key := range request.ElementKeys {
		keys[key] = struct{}{}
	}
	if request.ProjectID == "" || request.Page == "" || len(keys) < 2 {
		return fail(400, "请选择同一站点、同一页面的至少两条点击记录")
	}
	filter, args, err := s.filter(r)
	if err != nil {
		return err
	}
	elementKeys, err := json.Marshal(request.ElementKeys)
	if err != nil {
		return err
	}

	// Validate the current group keys and update their members in one write, so
	// an overlapping merge cannot silently change the scope of this selection.
	result, err := s.DB.ExecContext(r.Context(), clickGroupQuery(filter+" AND project_id=?")+`,
		selection AS (SELECT DISTINCT value AS element_key FROM json_each(?)),
		scope(project_id,page,group_key) AS (VALUES (?,?,json_array('merged',?)))
		INSERT INTO click_merge_members(project_id,page,element_key,group_key)
		SELECT scope.project_id,scope.page,COALESCE(m.element_key,k.element_key),scope.group_key
		FROM scope CROSS JOIN selection k
		LEFT JOIN click_merge_members m
			ON m.project_id=scope.project_id AND m.page=scope.page AND m.group_key=k.element_key
		WHERE (
			SELECT count(DISTINCT c.element_key)
			FROM click_groups c JOIN selection s ON c.element_key=s.element_key
			WHERE c.page=(SELECT page FROM scope)
		)=(SELECT count(*) FROM selection)
		ON CONFLICT(project_id,page,element_key) DO UPDATE SET group_key=excluded.group_key`,
		append(args, request.ProjectID, string(elementKeys), request.ProjectID, request.Page, Random())...)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return fail(409, "所选点击记录已变化，请刷新后重新选择")
	}
	s.audit("clicks.merge", request.ProjectID)
	jsonOut(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}
