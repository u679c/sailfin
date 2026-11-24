package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNginxRetriesWithoutUnsupportedErrorLogFlag(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "nginx")
	const script = `#!/bin/sh
if [ "$1" = "-e" ]; then
    echo 'nginx: invalid option: "e"' >&2
    exit 1
fi
test "$#" -eq 3 && test "$1" = "-c" && test "$2" = "/etc/nginx/nginx.conf" && test "$3" = "-T" || exit 2
printf 'configuration dump\n'
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	output, err := runNginx(context.Background(), binary, []string{"-e", "stderr", "-c", "/etc/nginx/nginx.conf"}, "-T")
	if err != nil || string(output) != "configuration dump\n" {
		t.Fatalf("unexpected fallback result: %q, %v", output, err)
	}
}

func TestAutomaticNginxPlanChangesOnlyCompatibleServers(t *testing.T) {
	const before = `server {
    listen 80;
    server_name _;
    location / {
        proxy_pass http://127.0.0.1:22993;
        proxy_set_header Host ${host};
    }
}
server {
    server_name redirect.example;
    return 301 https://example.com$request_uri;
}
`
	const relay = "127.0.0.1:8091"
	const auth = "/var/local/sailfin/.nginx-relay-auth.conf"
	after, reports, err := planAutomaticInjection(before, "/tmp/site.conf", relay, auth, "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 2 || reports[0].Status != "ready" || reports[1].Status != "skipped" {
		t.Fatalf("unexpected preview: %+v", reports)
	}
	redirect := strings.Index(before, "server {\n    server_name redirect")
	if !strings.HasSuffix(after, before[redirect:]) {
		t.Fatal("the unrelated redirect server changed")
	}
	for _, directive := range []string{
		"proxy_set_header Host ${host};",
		"proxy_set_header Accept-Encoding \"\";",
		"include \"" + auth + "\";",
		"/__sailfin/sailfin-boot.js",
	} {
		if !strings.Contains(after, directive) {
			t.Errorf("updated configuration lacks %q", directive)
		}
	}
	again, statuses, err := planAutomaticInjection(after, "/tmp/site.conf", relay, auth, "192.0.2.10")
	if err != nil || again != after || len(statuses) != 2 || statuses[0].Status != "configured" {
		t.Fatalf("repeated scans must be idempotent: %v, %+v", err, statuses)
	}
}

func TestNginxSiteOriginSupportsIPDeployment(t *testing.T) {
	tests := []struct {
		config, fallback, origin string
	}{
		{`listen 80; server_name 192.0.2.10;`, "", "http://192.0.2.10"},
		{`listen 192.0.2.10:8080; server_name _;`, "", "http://192.0.2.10:8080"},
		{`listen 192.0.2.10;`, "", "http://192.0.2.10"},
		{`listen [2001:db8::10]:8443 ssl;`, "", "https://[2001:db8::10]:8443"},
		{`listen 80; server_name _;`, "192.0.2.10", "http://192.0.2.10"},
		{`listen 80; server_name _;`, "", ""},
		{`listen 192.0.2.10:80; listen 192.0.2.11:80;`, "", ""},
		{`listen 80; server_name one.test two.test;`, "192.0.2.10", ""},
	}
	for _, tt := range tests {
		t.Run(tt.config+tt.fallback, func(t *testing.T) {
			nodes, err := parseNginx("server { " + tt.config + " }")
			if err != nil {
				t.Fatal(err)
			}
			id, origin := nginxSiteOrigin(nodes[0], tt.fallback)
			if origin != tt.origin || (id == "") != (tt.origin == "") {
				t.Fatalf("got (%q, %q), want origin %q", id, origin, tt.origin)
			}
		})
	}
}

func TestNginxMasterArgsPreservesPathsWithSpaces(t *testing.T) {
	args, err := nginxMasterArgs("/opt/homebrew/bin/nginx -c /Users/admin/code/Sailfin/nginx.conf")
	if err != nil || len(args) != 4 || args[2] != "-c" || args[3] != "/Users/admin/code/Sailfin/nginx.conf" {
		t.Fatalf("unexpected command args: %q, %v", args, err)
	}
	if _, err := nginxMasterArgs("nginx -g load_module /tmp/module.so;"); err == nil {
		t.Fatal("untrusted global Nginx directives must not be replayed")
	}
}
