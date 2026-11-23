package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"sailfin/internal/agent"
	"sailfin/internal/branding"
	"sailfin/internal/core"
)

func main() {
	branding.Environment()
	if err := command(os.Args[1:]); err != nil {
		slog.Error("Sailfin stopped", "error", err)
		os.Exit(1)
	}
}

func command(args []string) error {
	containerMode := false
	if len(args) > 0 {
		switch args[0] {
		case "aqua":
			return aquaCommand(args[1:])
		case "aqua-serve":
			return aquaServe(args[1:])
		case "aqua-replace":
			if len(args) != 2 {
				return errors.New("aqua-replace requires a staging directory")
			}
			return replaceAquaRelease(args[1])
		case "nginx-inject":
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return nginxInject(ctx, args[1:])
		case "serve":
			args = args[1:]
		case "container-serve":
			containerMode = true
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	key := ""
	if containerMode {
		raw, err := os.ReadFile(containerKeyPath)
		if err != nil {
			return err
		}
		if !validHostKey(string(raw)) {
			return errors.New("invalid Sailfin host probe key")
		}
		key = string(raw)
	}
	return run(ctx, options, key)
}

func run(ctx context.Context, options serveOptions, hostKey string) error {
	managed, err := configureManagedNginx(options.nginx)
	if err != nil {
		return err
	}
	dbPath := options.db
	if dbPath == "" {
		dbPath = branding.DefaultDBPath()
	}
	addr := options.addr
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer listener.Close()

	var auto *nginxAuto
	var bridge *hostBridge
	var nginx core.NginxIntegration
	if hostKey == "" {
		auto, err = newNginxAuto(filepath.Dir(dbPath), options.relayAddr)
		if err != nil {
			return err
		}
		nginx, err = hostNginxIntegration(ctx, options, auto)
		if err != nil {
			return err
		}
	} else {
		bridge = newHostBridge()
		nginx = bridge
	}
	s, err := core.Open(core.Config{
		Address: addr, DBPath: dbPath, PublicURL: options.publicURL,
		WebDir: options.webDir, AdminName: os.Getenv("SAILFIN_ADMIN_NAME"),
		AdminPassword: os.Getenv("SAILFIN_ADMIN_PASSWORD"), OfflineSeconds: options.offline,
		LocalOnly: true, Nginx: nginx,
	})
	if err != nil {
		return err
	}
	defer s.Close()

	ctx, stop := context.WithCancel(ctx)
	defer stop()
	handler := s.Handler()
	if bridge != nil {
		mux := http.NewServeMux()
		mux.Handle(hostBridgePath+"/", http.StripPrefix(hostBridgePath, bridge.handler(hostKey, s.ProbeHandler())))
		mux.Handle("/", handler)
		handler = mux
	}
	srv := &http.Server{
		Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second,
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
	maintenanceDone := make(chan struct{})
	go func() {
		defer close(maintenanceDone)
		s.Maintain(ctx.Done())
	}()
	var probeErr error
	probeDone := make(chan struct{})
	go func() {
		defer close(probeDone)
		if auto == nil {
			return
		}
		probeErr = runProbe(ctx, s.ProbeHandler(), filepath.Dir(dbPath), options, managed, auto, auto.ready.Store)
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
	slog.Info("Sailfin Core listening", "address", addr)
	err = srv.Serve(listener)
	stop()
	<-httpDone
	<-probeDone
	<-nginxDone
	<-maintenanceDone
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

func runProbe(ctx context.Context, handler http.Handler, dataDir string, options serveOptions, managed *managedNginx, auto *nginxAuto, relayState func(bool)) error {
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
	relay := auto.relay
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
		RelayAddress: relay, AutoBrowserSite: auto.browserSite, RelayState: relayState,
	}, core.LocalProbeID, handler)
	if err != nil {
		return err
	}
	defer probe.Close()
	return probe.Run(ctx)
}
