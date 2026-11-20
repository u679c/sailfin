package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"tracelite/internal/agent"
)

func split(v string) []string {
	out := []string{}
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
func main() {
	server := os.Getenv("TRACELITE_SERVER")
	if server == "" {
		server = "http://127.0.0.1:8080"
	}
	sites, err := agent.LoadSites(os.Getenv("TRACELITE_SITES_FILE"))
	if err != nil {
		slog.Error("Cannot load site configuration", "error", err)
		os.Exit(1)
	}
	a, e := agent.Open(agent.Options{Server: server, Sites: sites, RelayAddress: os.Getenv("TRACELITE_RELAY_ADDR"), StateDir: os.Getenv("TRACELITE_AGENT_STATE"), EnrollToken: os.Getenv("TRACELITE_ENROLL_TOKEN"), LogRoots: split(os.Getenv("TRACELITE_LOG_ROOTS")), DockerSocket: os.Getenv("TRACELITE_DOCKER_SOCKET"), DockerAllowed: split(os.Getenv("TRACELITE_DOCKER_ALLOWED")), AllowHTTP: os.Getenv("TRACELITE_ALLOW_HTTP") == "1"})
	if e != nil {
		slog.Error("Agent initialization failed", "error", e)
		os.Exit(1)
	}
	defer a.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if e = a.Run(ctx); e != nil {
		slog.Error("Agent stopped", "error", e)
		os.Exit(1)
	}
}
