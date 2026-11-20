package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"tracelite/internal/agent"
	"tracelite/internal/core"
)

func main() {
	if err := command(os.Args[1:]); err != nil {
		slog.Error("TraceLite stopped", "error", err)
		os.Exit(1)
	}
}

func command(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "nginx-inject":
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return nginxInject(ctx, args[1:])
		case "serve":
			args = args[1:]
		}
	}
	options, err := serveFlags(args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	return run(options)
}

func run(options serveOptions) error {
	managed, err := configureManagedNginx(options.nginx)
	if err != nil {
		return err
	}
	dbPath := options.db
	if dbPath == "" {
		dbPath = filepath.Join("var", "tracelite.db")
	}
	addr := options.addr
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	s, err := core.Open(core.Config{
		Address: addr, DBPath: dbPath, PublicURL: options.publicURL,
		WebDir: options.webDir, AdminName: os.Getenv("TRACELITE_ADMIN_NAME"),
		AdminPassword: os.Getenv("TRACELITE_ADMIN_PASSWORD"), OfflineSeconds: options.offline,
		LocalOnly: true,
	})
	if err != nil {
		return err
	}
	defer s.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv := &http.Server{
		Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 20 * time.Second, WriteTimeout: 30 * time.Second,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384,
	}
	var nginxResult <-chan error
	if managed != nil {
		nginxResult, err = managed.start(ctx)
		if err != nil {
			return err
		}
	}
	var nginxErr error
	nginxDone := make(chan struct{})
	go func() {
		defer close(nginxDone)
		if nginxResult != nil {
			nginxErr = <-nginxResult
			if nginxErr != nil {
				stop()
			}
		}
	}()
	go s.Maintain(ctx.Done())
	var probeErr error
	probeDone := make(chan struct{})
	go func() {
		defer close(probeDone)
		probeErr = runProbe(ctx, s, filepath.Dir(dbPath), options, managed)
		if probeErr != nil {
			s.ReportProbeFailure(probeErr)
			slog.Error("Local probe stopped", "error", probeErr)
			if managed != nil {
				stop()
			}
		}
	}()
	httpDone := make(chan struct{})
	go func() {
		defer close(httpDone)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdown); err != nil {
			slog.Warn("Graceful shutdown timed out", "error", err)
			_ = srv.Close()
		}
	}()
	slog.Info("TraceLite Core listening", "address", addr)
	err = srv.ListenAndServe()
	stop()
	<-httpDone
	<-probeDone
	<-nginxDone
	if nginxErr != nil {
		return nginxErr
	}
	if managed != nil && probeErr != nil {
		return probeErr
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func runProbe(ctx context.Context, s *core.Server, dataDir string, options serveOptions, managed *managedNginx) error {
	configPath := options.sitesFile
	if configPath == "" {
		defaultPath := filepath.Join(dataDir, "sites.json")
		if _, err := os.Stat(defaultPath); err == nil {
			configPath = defaultPath
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	sites, err := agent.LoadSites(configPath)
	if err != nil {
		return err
	}
	relay := options.relayAddr
	if managed != nil {
		sites, err = managed.addSite(sites)
		if err != nil {
			return err
		}
		relay = managed.relay
	}
	stateDir := options.probeState
	if stateDir == "" {
		stateDir = filepath.Join(dataDir, "local-probe")
	}
	roots := []string{}
	for _, part := range strings.Split(options.logRoots, ",") {
		if dir := strings.TrimSpace(part); dir != "" {
			roots = append(roots, dir)
		}
	}
	probe, err := agent.OpenLocal(agent.Options{
		StateDir: stateDir, Sites: sites, LogRoots: roots,
		RelayAddress: relay,
	}, core.LocalProbeID, s.ProbeHandler())
	if err != nil {
		return err
	}
	defer probe.Close()
	return probe.Run(ctx)
}
