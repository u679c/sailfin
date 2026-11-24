package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"sailfin/internal/agent"
	"sailfin/internal/branding"
	"sailfin/internal/protocol"
)

func nginxInject(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("nginx-inject", flag.ContinueOnError)
	sitePath := flags.String("site-file", "", "Nginx file containing the existing server block")
	server := flags.String("server-name", "", "exact existing server_name")
	listen := flags.String("listen", "", "listen argument to distinguish multiple matching servers")
	location := flags.String("location", "/", "HTML-producing location arguments")
	origin := flags.String("origin", "", "existing public HTTP(S) origin")
	id := flags.String("site-id", "", "stable Sailfin site ID")
	name := flags.String("site-name", "", "site display name")
	sitesPath := flags.String("sites-file", "var/sites.json", "site configuration read by Core")
	relay := flags.String("relay-addr", "127.0.0.1:8091", "local probe address")
	conf := flags.String("nginx-config", "", "running Nginx main config, if non-default")
	prefix := flags.String("nginx-prefix", "", "running Nginx prefix, if non-default")
	binary := flags.String("nginx-bin", "nginx", "Nginx executable")
	localTest := flags.Bool("local-test", false, "collect without a consent event on a loopback test site")
	apply := flags.Bool("apply", false, "write changes, validate and reload Nginx")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 || *sitePath == "" || *server == "" || *origin == "" || !protocol.ValidSiteID(*id) {
		return errors.New("--site-file, --server-name, --origin, and a valid --site-id are required")
	}
	u, err := url.Parse(*origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() != *server || u.User != nil ||
		*origin != u.Scheme+"://"+u.Host {
		return errors.New("--origin must be an exact HTTP(S) origin matching --server-name, with no trailing slash")
	}
	if *localTest && !isLoopbackHost(u.Hostname()) {
		return errors.New("--local-test is only available for loopback origins")
	}
	address, err := loopbackAddress(*relay)
	if err != nil {
		return fmt.Errorf("--relay-addr: %w", err)
	}
	displayName := strings.TrimSpace(*name)
	if displayName == "" {
		displayName = *server
	}
	if len(displayName) > 120 {
		return errors.New("--site-name exceeds 120 bytes")
	}

	target, err := readDeploymentFile(*sitePath, false)
	if err != nil {
		return err
	}
	siteConfig, err := readDeploymentFile(*sitesPath, true)
	if err != nil {
		return err
	}
	if target.path == siteConfig.path {
		return errors.New("Nginx and probe configurations must be separate files")
	}
	updated, changed, err := editNginxSite(string(target.before), *server, *listen, *location, *id, address)
	if err != nil {
		return err
	}
	siteJSON, err := planSiteConfig(siteConfig, agent.Site{
		ID: *id, Name: displayName, BrowserOrigin: *origin, RequireConsent: !*localTest,
	})
	if err != nil {
		return err
	}
	fmt.Printf("Nginx: %s, server %s, location %s\n", target.path, *server, *location)
	fmt.Printf("SDK: %s%s → probe %s\n", *origin, branding.BrowserBootPath, address)
	fmt.Printf("Sailfin site: %s → %s\n", *id, siteConfig.path)
	if !*apply {
		fmt.Println("Preview only. Use --apply to write the files, validate and reload Nginx.")
		return nil
	}

	command, err := exec.LookPath(*binary)
	if err != nil {
		return err
	}
	var base []string
	for _, item := range []struct{ option, value string }{{"-p", *prefix}, {"-c", *conf}} {
		if item.value != "" {
			value, err := filepath.Abs(item.value)
			if err != nil {
				return err
			}
			base = append(base, item.option, value)
		}
	}
	configDump, err := runNginx(ctx, command, base, "-T")
	if err != nil {
		return err
	}
	if !nginxIncludesFile(string(configDump), target.path) {
		return errors.New("the selected file is not loaded by this Nginx config; check --nginx-config and --nginx-prefix")
	}

	if !changed && bytes.Equal(siteConfig.before, siteJSON) {
		fmt.Println("Already configured.")
		return nil
	}
	target.after = []byte(updated)
	siteConfig.after = siteJSON
	files := []*deploymentFile{&target, &siteConfig}
	var written []*deploymentFile
	rollback := func(cause error) error {
		for i := len(written) - 1; i >= 0; i-- {
			if err := written[i].restore(); err != nil {
				cause = errors.Join(cause, fmt.Errorf("restore %s from %s: %w", written[i].path, written[i].backup, err))
			}
		}
		return cause
	}
	for _, file := range files {
		if bytes.Equal(file.before, file.after) {
			continue
		}
		if err := file.apply(); err != nil {
			return rollback(err)
		}
		written = append(written, file)
	}
	if _, err := runNginx(ctx, command, base, "-t"); err != nil {
		return rollback(err)
	}
	if changed {
		if _, err := runNginx(ctx, command, base, "-s", "reload"); err != nil {
			return rollback(err)
		}
		fmt.Println("Nginx configuration validated and reload requested.")
	}
	fmt.Printf("Start or restart Core with --sites-file %q --relay-addr %q.\n", siteConfig.path, address)
	return nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func planSiteConfig(file deploymentFile, site agent.Site) ([]byte, error) {
	var sites []agent.Site
	if file.exists {
		var err error
		sites, err = agent.DecodeSites(file.before)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file.path, err)
		}
	}
	found := false
	for i, existing := range sites {
		if existing.ID != site.ID {
			continue
		}
		if found || (existing.BrowserOrigin != "" && existing.BrowserOrigin != site.BrowserOrigin) {
			return nil, fmt.Errorf("site ID %s is already assigned; choose another --site-id", site.ID)
		}
		site.AccessLog = existing.AccessLog
		sites[i], found = site, true
	}
	if !found {
		if len(sites) >= 50 {
			return nil, errors.New("the local probe supports at most 50 configured sites")
		}
		sites = append(sites, site)
	}
	if err := agent.ValidateSites(sites); err != nil {
		return nil, err
	}
	body, err := json.MarshalIndent(sites, "", "  ")
	if err != nil {
		return nil, err
	}
	if len(body)+1 > 64<<10 {
		return nil, errors.New("site config exceeds 64 KiB")
	}
	return append(body, '\n'), nil
}

func runNginx(ctx context.Context, command string, base []string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, append(append([]string{}, base...), args...)...)
	output, err := cmd.Output()
	var exit *exec.ExitError
	if len(base) >= 2 && base[0] == "-e" && base[1] == "stderr" &&
		errors.As(err, &exit) && bytes.Contains(exit.Stderr, []byte(`invalid option: "e"`)) {
		// Some installed Nginx versions support -T but lack the error-log override.
		cmd = exec.CommandContext(ctx, command, append(append([]string{}, base[2:]...), args...)...)
		output, err = cmd.Output()
	}
	if err != nil {
		if errors.As(err, &exit) {
			return nil, fmt.Errorf("nginx %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(exit.Stderr)))
		}
		return nil, err
	}
	return output, nil
}

func nginxIncludesFile(dump, expected string) bool {
	for _, line := range strings.Split(dump, "\n") {
		path, ok := strings.CutPrefix(line, "# configuration file ")
		if !ok || !strings.HasSuffix(path, ":") {
			continue
		}
		actual, err := filepath.EvalSymlinks(strings.TrimSuffix(path, ":"))
		if err == nil && actual == expected {
			return true
		}
	}
	return false
}

type deploymentFile struct {
	path, backup  string
	before, after []byte
	mode          os.FileMode
	owner, group  int
	exists        bool
}

func readDeploymentFile(path string, allowMissing bool) (deploymentFile, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return deploymentFile{}, err
	}
	resolved, err := filepath.EvalSymlinks(path)
	exists := err == nil
	if exists {
		path = resolved
	} else if allowMissing && os.IsNotExist(err) {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			return deploymentFile{}, fmt.Errorf("%s does not resolve to a regular file", path)
		}
		dir, err := filepath.EvalSymlinks(filepath.Dir(path))
		if err != nil {
			return deploymentFile{}, err
		}
		path = filepath.Join(dir, filepath.Base(path))
	} else {
		return deploymentFile{}, err
	}
	statPath := path
	if !exists {
		statPath = filepath.Dir(path)
	}
	info, err := os.Stat(statPath)
	if err != nil {
		return deploymentFile{}, err
	}
	owner := info.Sys().(*syscall.Stat_t)
	file := deploymentFile{path: path, mode: 0600, owner: int(owner.Uid), group: int(owner.Gid), exists: exists}
	if exists {
		if !info.Mode().IsRegular() || info.Size() > 2<<20 {
			return deploymentFile{}, fmt.Errorf("%s must be a regular file of at most 2 MiB", path)
		}
		file.mode = info.Mode().Perm()
		file.before, err = os.ReadFile(path)
	}
	return file, err
}

func (f *deploymentFile) verifyUnchanged() error {
	current, err := os.ReadFile(f.path)
	if f.exists && (err != nil || !bytes.Equal(current, f.before)) ||
		!f.exists && !os.IsNotExist(err) {
		return fmt.Errorf("%s changed while preparing the deployment; retry", f.path)
	}
	return nil
}

func (f *deploymentFile) apply() error {
	if err := f.verifyUnchanged(); err != nil {
		return err
	}
	if f.exists {
		backup, err := os.CreateTemp(filepath.Dir(f.path), ".sailfin-backup-*")
		if err != nil {
			return err
		}
		f.backup = backup.Name()
		if _, err := backup.Write(f.before); err != nil {
			_ = backup.Close()
			return err
		}
		if err := backup.Close(); err != nil {
			return err
		}
		fmt.Printf("Backup: %s\n", f.backup)
	}
	return f.replace(f.after)
}

func (f *deploymentFile) restore() error {
	current, err := os.ReadFile(f.path)
	if err != nil || !bytes.Equal(current, f.after) {
		return errors.New("file changed after applying; inspect the backup before restoring")
	}
	if !f.exists {
		return os.Remove(f.path)
	}
	return f.replace(f.before)
}

func (f *deploymentFile) replace(body []byte) error {
	file, err := os.CreateTemp(filepath.Dir(f.path), ".sailfin-write-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	stat := info.Sys().(*syscall.Stat_t)
	if int(stat.Uid) != f.owner || int(stat.Gid) != f.group {
		if err := file.Chown(f.owner, f.group); err != nil {
			return err
		}
	}
	if err := file.Chmod(f.mode); err != nil {
		return err
	}
	if _, err := file.Write(body); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), f.path)
}
