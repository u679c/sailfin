package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"

	"sailfin/internal/core"
)

const hostBridgePath = "/_sailfin_host"

type nginxJob struct {
	ID       string
	PlanID   string
	SiteIDs  []string
	Deadline time.Time
}

type nginxResult struct {
	Preview core.NginxPreview
	Error   string
}

type hostBridge struct {
	jobs    chan nginxJob
	mu      sync.Mutex
	pending map[string]chan nginxResult
}

func newHostBridge() *hostBridge {
	return &hostBridge{jobs: make(chan nginxJob), pending: make(map[string]chan nginxResult)}
}

func (b *hostBridge) Scan(ctx context.Context) (core.NginxPreview, error) {
	return b.request(ctx, "", nil)
}

func (b *hostBridge) Apply(ctx context.Context, id string, selected []string) (core.NginxPreview, error) {
	if id == "" {
		return core.NginxPreview{}, errors.New("请选择有效的 Nginx 接入预览")
	}
	return b.request(ctx, id, selected)
}

func (b *hostBridge) request(ctx context.Context, planID string, selected []string) (core.NginxPreview, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(15 * time.Second)
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	job := nginxJob{ID: core.Random(), PlanID: planID, SiteIDs: selected, Deadline: deadline}
	reply := make(chan nginxResult, 1)
	b.mu.Lock()
	if len(b.pending) != 0 {
		b.mu.Unlock()
		return core.NginxPreview{}, errors.New("已有 Nginx 接入操作正在执行")
	}
	b.pending[job.ID] = reply
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.pending, job.ID)
		b.mu.Unlock()
	}()

	dispatch, cancelDispatch := context.WithTimeout(ctx, 4*time.Second)
	defer cancelDispatch()
	select {
	case b.jobs <- job:
	case <-dispatch.Done():
		return core.NginxPreview{}, errors.New("宿主机探针未连接，请检查 aqua status")
	}
	select {
	case result := <-reply:
		if result.Error != "" {
			return core.NginxPreview{}, errors.New(result.Error)
		}
		return result.Preview, nil
	case <-ctx.Done():
		return core.NginxPreview{}, ctx.Err()
	}
}

func (b *hostBridge) handler(key string, probe http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /nginx/next", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		defer cancel()
		select {
		case job := <-b.jobs:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(job)
		case <-ctx.Done():
			w.WriteHeader(http.StatusNoContent)
		}
	})
	mux.HandleFunc("POST /nginx/result/{id}", func(w http.ResponseWriter, r *http.Request) {
		var result nginxResult
		body := http.MaxBytesReader(w, r.Body, 1<<20)
		if err := json.NewDecoder(body).Decode(&result); err != nil {
			http.Error(w, "invalid result", http.StatusBadRequest)
			return
		}
		b.mu.Lock()
		reply := b.pending[r.PathValue("id")]
		b.mu.Unlock()
		if reply != nil {
			select {
			case reply <- result:
			default:
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.Handle("/", probe)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+key)) != 1 {
			http.NotFound(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func bridgeProbeHandler(coreURL, key string, transport http.RoundTripper) http.Handler {
	target, _ := url.Parse(coreURL + hostBridgePath)
	return &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(request *httputil.ProxyRequest) {
			request.SetURL(target)
			request.Out.Header.Set("Authorization", "Bearer "+key)
		},
	}
}

func pollNginx(ctx context.Context, client *http.Client, coreURL, key string, nginx core.NginxIntegration) {
	for ctx.Err() == nil {
		if err := handleNextNginxJob(ctx, client, coreURL+hostBridgePath, key, nginx); err != nil && ctx.Err() == nil {
			slog.Warn("Host probe Nginx channel", "error", err)
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
	}
}

func handleNextNginxJob(ctx context.Context, client *http.Client, baseURL, key string, nginx core.NginxIntegration) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/nginx/next", nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+key)
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNoContent {
		return nil
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Core returned HTTP %d", response.StatusCode)
	}
	var job nginxJob
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&job); err != nil {
		return err
	}
	result := nginxResult{}
	operation, cancel := context.WithDeadline(ctx, job.Deadline)
	defer cancel()
	if job.PlanID == "" {
		result.Preview, err = nginx.Scan(operation)
	} else {
		result.Preview, err = nginx.Apply(operation, job.PlanID, job.SiteIDs)
	}
	if err != nil {
		result.Error = err.Error()
	}
	body, err := json.Marshal(result)
	if err != nil {
		return err
	}
	reply, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/nginx/result/"+url.PathEscape(job.ID), bytes.NewReader(body))
	if err != nil {
		return err
	}
	reply.Header.Set("Authorization", "Bearer "+key)
	reply.Header.Set("Content-Type", "application/json")
	ack, err := client.Do(reply)
	if err != nil {
		return err
	}
	defer ack.Body.Close()
	if ack.StatusCode != http.StatusNoContent {
		return fmt.Errorf("Core returned HTTP %d", ack.StatusCode)
	}
	return nil
}
