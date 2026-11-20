package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"tracelite/internal/agent"
	"tracelite/internal/protocol"
)

type managedNginx struct {
	upstream string
	listen   string
	relay    string
	site     agent.Site
}

type managedNginxOptions struct {
	upstream, port, relay, id, name, consent string
}

func configureManagedNginx(o managedNginxOptions) (*managedNginx, error) {
	raw := o.upstream
	if raw == "" {
		return nil, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Host == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.ForceQuery || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("--nginx-upstream must be an HTTP origin on this host")
	}
	port := u.Port()
	if port == "" {
		port = "80"
	}
	host := u.Hostname()
	if host == "localhost" {
		host = "127.0.0.1"
	}
	upstream, err := loopbackAddress(net.JoinHostPort(host, port))
	if err != nil {
		return nil, fmt.Errorf("--nginx-upstream: %w", err)
	}
	listenPort := o.port
	if listenPort == "" {
		listenPort = "9080"
	}
	n, err := strconv.Atoi(listenPort)
	if err != nil || n < 1024 || n > 65535 {
		return nil, errors.New("--nginx-port must be between 1024 and 65535")
	}
	listen := net.JoinHostPort("127.0.0.1", strconv.Itoa(n))

	relay := o.relay
	if relay == "" {
		relay = "127.0.0.1:8091"
	}
	relay, err = loopbackAddress(relay)
	if err != nil {
		return nil, fmt.Errorf("--relay-addr: %w", err)
	}
	if listen == upstream || listen == relay || relay == upstream {
		return nil, errors.New("Nginx entry, business upstream, and probe relay must use distinct addresses")
	}

	id := o.id
	if id == "" {
		id = "local-web"
	}
	if !protocol.ValidSiteID(id) {
		return nil, errors.New("--nginx-site-id is invalid")
	}
	name := strings.TrimSpace(o.name)
	if name == "" {
		name = id
	}
	if len(name) > 120 {
		return nil, errors.New("--nginx-site-name exceeds 120 bytes")
	}
	consent := o.consent
	if consent != "" && consent != "0" && consent != "1" {
		return nil, errors.New("--nginx-require-consent must be 0 or 1")
	}
	return &managedNginx{
		upstream: upstream, listen: listen, relay: relay,
		site: agent.Site{
			ID: id, Name: name, BrowserOrigin: "http://localhost:" + strconv.Itoa(n),
			RequireConsent: consent != "0",
		},
	}, nil
}

func loopbackAddress(address string) (string, error) {
	host, rawPort, err := net.SplitHostPort(address)
	if err != nil {
		return "", errors.New("a loopback IP and port are required")
	}
	ip := net.ParseIP(host)
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 || ip == nil || !ip.IsLoopback() {
		return "", errors.New("a loopback IP and valid port are required")
	}
	return net.JoinHostPort(ip.String(), strconv.Itoa(port)), nil
}

func (c *managedNginx) addSite(sites []agent.Site) ([]agent.Site, error) {
	for i, site := range sites {
		if site.ID != c.site.ID {
			continue
		}
		if site.BrowserOrigin != "" && site.BrowserOrigin != c.site.BrowserOrigin {
			return nil, fmt.Errorf("site %s has a different browserOrigin in sites.json; use a distinct managed site ID", site.ID)
		}
		sites[i].BrowserOrigin = c.site.BrowserOrigin
		sites[i].RequireConsent = c.site.RequireConsent
		return sites, nil
	}
	return append(sites, c.site), nil
}

func (c *managedNginx) start(ctx context.Context) (<-chan error, error) {
	nginx, err := exec.LookPath("nginx")
	if err != nil {
		return nil, errors.New("automatic configuration requires nginx in PATH")
	}
	dir, err := os.MkdirTemp("", "tracelite-nginx-")
	if err != nil {
		return nil, err
	}
	started := false
	defer func() {
		if !started {
			_ = os.RemoveAll(dir)
		}
	}()
	if err := os.WriteFile(filepath.Join(dir, "nginx.conf"), []byte(c.config()), 0600); err != nil {
		return nil, err
	}
	args := []string{"-e", "stderr", "-p", dir + "/", "-c", "nginx.conf"}
	test := exec.CommandContext(ctx, nginx, append(append([]string{}, args...), "-t")...)
	if output, err := test.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("nginx -t failed: %w: %s", err, strings.TrimSpace(string(output)))
	}

	cmd := exec.CommandContext(ctx, nginx, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGQUIT) }
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	started = true
	slog.Info("Starting managed Nginx", "url", c.site.BrowserOrigin, "upstream", c.upstream)
	done := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		_ = os.RemoveAll(dir)
		if ctx.Err() != nil {
			err = nil
		} else if err == nil {
			err = errors.New("managed Nginx exited unexpectedly")
		} else {
			err = fmt.Errorf("managed Nginx exited: %w", err)
		}
		done <- err
		close(done)
	}()
	return done, nil
}

func (c *managedNginx) config() string {
	// All substitutions are validated IP addresses, ports or a stable site ID.
	return fmt.Sprintf(`daemon off;
master_process off;
pid nginx.pid;
lock_file nginx.lock;
error_log stderr warn;
events { worker_connections 128; }
http {
    access_log off;
    client_body_temp_path client-body;
    proxy_temp_path proxy;
    fastcgi_temp_path fastcgi;
    uwsgi_temp_path uwsgi;
    scgi_temp_path scgi;
    server {
        listen %[1]s;
        server_name localhost;

        location ^~ /__tracelite/ {
            client_max_body_size 64k;
            client_body_buffer_size 64k;
            proxy_pass http://%[2]s;
            proxy_set_header X-TraceLite-Site %[3]s;
            proxy_set_header Authorization "";
            proxy_set_header Cookie "";
            proxy_read_timeout 10s;
        }

        location / {
            proxy_pass http://%[4]s;
            proxy_set_header Host $http_host;
            proxy_set_header Accept-Encoding "";
            sub_filter '</head>' '<script type="module" src="/__tracelite/boot.js"></script></head>';
            sub_filter_once on;
        }
    }
}
`, c.listen, c.relay, c.site.ID, c.upstream)
}
