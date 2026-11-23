package core

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"sailfin/internal/protocol"
)

type openRestySDKFixture struct {
	row NginxSiteResult
}

func (f openRestySDKFixture) Scan(context.Context) (NginxPreview, error) {
	return NginxPreview{ID: "sdk-preview", Sites: []NginxSiteResult{f.row}}, nil
}

func (f openRestySDKFixture) Apply(_ context.Context, _ string, _ []string) (NginxPreview, error) {
	row := f.row
	row.Status = "applied"
	return NginxPreview{ID: "sdk-preview", Sites: []NginxSiteResult{row}}, nil
}

func TestOpenRestyAddsSDKToExistingLogSite(t *testing.T) {
	h := newHarness(t)
	site := protocol.Site{ID: "example.test", Name: "Existing site", AccessLog: "/logs/example.log", RequireConsent: true}
	if err := h.s.storeSites(context.Background(), []protocol.Site{site}, "manual", false); err != nil {
		t.Fatal(err)
	}
	origin := "https://example.test"
	h.s.cfg.Nginx = openRestySDKFixture{row: NginxSiteResult{
		SiteID: site.ID, Site: site.ID, Origin: origin, Status: "ready", OpenResty: true,
	}}

	result := httptest.NewRecorder()
	if err := h.s.scanNginx(result, httptest.NewRequest("POST", "/", nil)); err != nil {
		t.Fatal(err)
	}
	var preview NginxPreview
	if err := json.Unmarshal(result.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.Ready != 1 || len(preview.Sites) != 1 || !preview.Sites[0].Added || !preview.Sites[0].NeedsSDK {
		t.Fatalf("existing log site should be selectable for SDK setup: %+v", preview)
	}
	request := httptest.NewRequest("POST", "/", strings.NewReader(`{"id":"sdk-preview","siteIds":["example.test"]}`))
	if err := h.s.applyNginx(httptest.NewRecorder(), request); err != nil {
		t.Fatal(err)
	}
	var name, path, actualOrigin string
	var enabled, consent bool
	err := h.s.DB.QueryRow(`SELECT p.name,s.access_log,s.browser_origin,s.enabled,s.require_consent
		FROM site_registry s JOIN projects p ON p.id=s.project_id WHERE s.project_id=?`,
		protocol.SiteProjectID(site.ID)).Scan(&name, &path, &actualOrigin, &enabled, &consent)
	if err != nil {
		t.Fatal(err)
	}
	if name != site.Name || path != site.AccessLog || actualOrigin != origin || enabled || !consent {
		t.Fatalf("SDK setup changed other site settings: %q %q %q %t %t", name, path, actualOrigin, enabled, consent)
	}
}
