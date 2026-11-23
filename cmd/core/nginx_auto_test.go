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
	after, reports, err := planAutomaticInjection(before, "/tmp/site.conf", relay, auth)
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
		"/__sailfin/boot.js",
	} {
		if !strings.Contains(after, directive) {
			t.Errorf("updated configuration lacks %q", directive)
		}
	}
	again, statuses, err := planAutomaticInjection(after, "/tmp/site.conf", relay, auth)
	if err != nil || again != after || len(statuses) != 2 || statuses[0].Status != "configured" {
		t.Fatalf("repeated scans must be idempotent: %v, %+v", err, statuses)
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
