package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
)

type serveOptions struct {
	addr, db, publicURL, webDir string
	sitesFile, relayAddr        string
	probeState, logRoots        string
	offline                     int
	nginx                       managedNginxOptions
}

func serveFlags(args []string) (serveOptions, error) {
	var o serveOptions
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	offline, _ := strconv.Atoi(os.Getenv("TRACELITE_OFFLINE_SECONDS"))
	flags.StringVar(&o.addr, "addr", os.Getenv("TRACELITE_ADDR"), "Core listen address")
	flags.StringVar(&o.db, "db", os.Getenv("TRACELITE_DB"), "SQLite database path")
	flags.StringVar(&o.publicURL, "public-url", os.Getenv("TRACELITE_PUBLIC_URL"), "management UI origin")
	flags.StringVar(&o.webDir, "web-dir", os.Getenv("TRACELITE_WEB_DIR"), "Web static files directory")
	flags.StringVar(&o.sitesFile, "sites-file", os.Getenv("TRACELITE_SITES_FILE"), "probe sites JSON file")
	flags.StringVar(&o.relayAddr, "relay-addr", os.Getenv("TRACELITE_RELAY_ADDR"), "local probe listen address")
	flags.StringVar(&o.probeState, "probe-state", os.Getenv("TRACELITE_PROBE_STATE"), "probe state directory")
	flags.StringVar(&o.logRoots, "log-roots", os.Getenv("TRACELITE_LOG_ROOTS"), "comma-separated authorized log directories")
	flags.IntVar(&o.offline, "offline-seconds", offline, "probe heartbeat timeout in seconds")
	flags.StringVar(&o.nginx.upstream, "nginx-upstream", os.Getenv("TRACELITE_NGINX_UPSTREAM"), "optional isolated Nginx upstream")
	flags.StringVar(&o.nginx.port, "nginx-port", os.Getenv("TRACELITE_NGINX_PORT"), "isolated Nginx port")
	flags.StringVar(&o.nginx.id, "nginx-site-id", os.Getenv("TRACELITE_NGINX_SITE_ID"), "isolated Nginx site ID")
	flags.StringVar(&o.nginx.name, "nginx-site-name", os.Getenv("TRACELITE_NGINX_SITE_NAME"), "isolated Nginx site name")
	flags.StringVar(&o.nginx.consent, "nginx-require-consent", os.Getenv("TRACELITE_NGINX_REQUIRE_CONSENT"), "isolated Nginx consent requirement (0 or 1)")
	if err := flags.Parse(args); err != nil {
		return o, err
	}
	if flags.NArg() != 0 {
		return o, fmt.Errorf("unexpected serve argument %q", flags.Arg(0))
	}
	o.nginx.relay = o.relayAddr
	return o, nil
}
