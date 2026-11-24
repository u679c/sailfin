package agent

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"

	browsersdk "sailfin/browser-agent"
	"sailfin/internal/branding"
	"sailfin/internal/protocol"
)

const relayPrefix = branding.BrowserRelayPrefix

func (a *Agent) startRelay() (*http.Server, error) {
	if a.opt.AutoBrowserSite != nil {
		return a.listenRelay()
	}
	for _, site := range a.sites {
		if site.BrowserOrigin != "" {
			return a.listenRelay()
		}
	}
	return nil, nil
}

func (a *Agent) listenRelay() (*http.Server, error) {
	address := a.opt.RelayAddress
	if address == "" {
		address = "127.0.0.1:8091"
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, errors.New("the Nginx relay must listen on a loopback address")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	srv := &http.Server{
		Handler: a.relayHandler(), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 20 * time.Second,
		IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10,
	}
	go func() {
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("Nginx relay stopped", "error", err)
		}
	}()
	slog.Info("Nginx relay listening", "address", listener.Addr().String())
	return srv, nil
}

func (a *Agent) relayHandler() http.Handler {
	slots := make(chan struct{}, 16)
	// The two modules share a version so a policy-only change also moves the entry URL.
	fingerprint := md5.New()
	fmt.Fprintf(fingerprint, "index.js:%d:", len(browsersdk.Index))
	_, _ = fingerprint.Write(browsersdk.Index)
	fmt.Fprintf(fingerprint, "policy.js:%d:", len(browsersdk.Policy))
	_, _ = fingerprint.Write(browsersdk.Policy)
	version := fmt.Sprintf("%x", fingerprint.Sum(nil))
	moduleDir := "sdk/" + version + "/"
	modules := map[string][]byte{
		relayPrefix + moduleDir + "index.js":  browsersdk.Index,
		relayPrefix + moduleDir + "policy.js": browsersdk.Policy,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		state := a.relaySites.Load()
		sites := state.sites
		site, ok := sites[branding.Header(r, "Site")]
		if branding.Header(r, "Auto") != "" && a.opt.AutoBrowserSite != nil {
			site, ok = a.opt.AutoBrowserSite(r)
			if configured, exists := sites[site.ID]; ok && exists {
				ok = configured.BrowserOrigin == site.BrowserOrigin
				site = configured
			}
		}
		if !ok || site.BrowserOrigin == "" {
			http.NotFound(w, r)
			return
		}
		prefix := relayPrefix
		if strings.HasPrefix(r.URL.Path, branding.LegacyRelayPrefix) {
			prefix = branding.LegacyRelayPrefix
		}
		path := relayPrefix + strings.TrimPrefix(r.URL.Path, prefix)
		switch r.Method + " " + path {
		case "GET " + branding.PreviousBrowserBootPath:
			http.Redirect(w, r, prefix+strings.TrimPrefix(branding.BrowserBootPath, relayPrefix), http.StatusTemporaryRedirect)
		case "GET " + branding.BrowserBootPath:
			w.Header().Set("X-Sailfin-SDK-Version", version)
			a.serveBoot(w, site, prefix, moduleDir)
		case "GET " + relayPrefix + moduleDir + "index.js", "GET " + relayPrefix + moduleDir + "policy.js":
			w.Header().Set("X-Sailfin-SDK-Version", version)
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = w.Write(modules[path])
		case "GET " + relayPrefix + "config":
			w.Header().Set("Content-Type", "application/json")
			projectID := protocol.SiteProjectID(site.ID)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"enabled": state.enabled == nil || state.enabled[projectID], "batchSize": 20, "intervalSeconds": 5,
				"clickSelectors": state.clickSelectors[projectID],
			})
		case "POST " + relayPrefix + "events":
			if !a.siteEnabled(site.ID) {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
				a.forwardBrowserEvents(w, r, site)
			default:
				http.Error(w, "relay busy", http.StatusServiceUnavailable)
			}
		default:
			http.NotFound(w, r)
		}
	})
}

func (a *Agent) serveBoot(w http.ResponseWriter, site Site, prefix, moduleDir string) {
	options, _ := json.Marshal(map[string]string{
		"projectId": site.ID, "endpoint": prefix + "events",
		"configEndpoint": prefix + "config",
	})
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	_, _ = fmt.Fprintf(w, `import { init } from %q;
let sdk;
function start() { if (!sdk) sdk = init(%s); }
if (!%t || window.SAILFIN_CONSENT === true || window.TRACELITE_CONSENT === true) start();
window.addEventListener("sailfin:consent", start);
window.addEventListener("tracelite:consent", start);
function revoke() { sdk?.stop(); sdk = undefined; }
window.addEventListener("sailfin:revoke", revoke);
window.addEventListener("tracelite:revoke", revoke);
`, prefix+moduleDir+"index.js", options, site.RequireConsent)
}

func (a *Agent) forwardBrowserEvents(w http.ResponseWriter, r *http.Request, site Site) {
	if r.Header.Get("Origin") != site.BrowserOrigin {
		http.Error(w, "origin is not allowed", http.StatusForbidden)
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		http.Error(w, "expected JSON", http.StatusUnsupportedMediaType)
		return
	}
	var payload struct {
		Events []protocol.Event `json:"events"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil || decoder.Decode(new(any)) != io.EOF || len(payload.Events) == 0 || len(payload.Events) > 20 {
		http.Error(w, "invalid event batch", http.StatusBadRequest)
		return
	}
	for i := range payload.Events {
		payload.Events[i].ProjectID = ""
		payload.Events[i].UA = r.UserAgent()
	}
	batch := protocol.SiteBatch{SiteID: site.ID, Name: site.Name, Source: "browser", Events: payload.Events}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	if err := a.request(ctx, http.MethodPost, "/api/v1/agent/sites/batch", batch, nil); err != nil {
		var response HTTPError
		status := http.StatusBadGateway
		if errors.As(err, &response) {
			switch response.Status {
			case 400, 413, 429:
				status = response.Status
			case 403:
				status = http.StatusServiceUnavailable
			}
		}
		http.Error(w, "event forwarding failed", status)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
