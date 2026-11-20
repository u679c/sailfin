package agent

import (
	"context"
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

	browsersdk "tracelite/browser-agent"
	"tracelite/internal/protocol"
)

const relayPrefix = "/__tracelite/"

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
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		site, ok := a.sites[r.Header.Get("X-TraceLite-Site")]
		if r.Header.Get("X-TraceLite-Auto") != "" && a.opt.AutoBrowserSite != nil {
			site, ok = a.opt.AutoBrowserSite(r)
		}
		if !ok || site.BrowserOrigin == "" {
			http.NotFound(w, r)
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "GET " + relayPrefix + "boot.js":
			a.serveBoot(w, site)
		case "GET " + relayPrefix + "index.js", "GET " + relayPrefix + "policy.js":
			raw, err := browsersdk.Files.ReadFile("src/" + strings.TrimPrefix(r.URL.Path, relayPrefix))
			if err != nil {
				http.Error(w, "SDK unavailable", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = w.Write(raw)
		case "GET " + relayPrefix + "config":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"enabled":true,"batchSize":20,"intervalSeconds":5}`)
		case "POST " + relayPrefix + "events":
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

func (a *Agent) serveBoot(w http.ResponseWriter, site Site) {
	options, _ := json.Marshal(map[string]string{
		"projectId": site.ID, "endpoint": relayPrefix + "events",
		"configEndpoint": relayPrefix + "config",
	})
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	_, _ = fmt.Fprintf(w, `import { init } from "./index.js";
let sdk;
function start() { if (!sdk) sdk = init(%s); }
if (!%t || window.TRACELITE_CONSENT === true) start();
window.addEventListener("tracelite:consent", start);
window.addEventListener("tracelite:revoke", () => { sdk?.stop(); sdk = undefined; });
`, options, site.RequireConsent)
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
