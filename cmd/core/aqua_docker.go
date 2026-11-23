package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"sailfin/internal/branding"
	"sailfin/internal/core"
	"sailfin/internal/update"
)

const containerKeyPath = "/run/sailfin-probe-key"

var dockerDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)
var packagedImageTag = regexp.MustCompile(`^sailfin-package:[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127}$`)

func runDocker(ctx context.Context, options serveOptions, ready func()) (runErr error) {
	address := options.addr
	if address == "" {
		address = "127.0.0.1:8080"
	}
	address, err := loopbackAddress(address)
	if err != nil {
		return fmt.Errorf("管理端发布地址：%w", err)
	}
	coreURL := "http://" + address
	if options.publicURL == "" {
		host, port, _ := net.SplitHostPort(address)
		if host == "127.0.0.1" {
			host = "localhost"
		}
		options.publicURL = "http://" + net.JoinHostPort(host, port)
	}

	docker, err := localDocker(ctx)
	if err != nil {
		return err
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	keyPath := filepath.Join(root, aquaDir, "probe.key")
	key, err := loadHostKey(keyPath)
	if err != nil {
		return err
	}
	installation := installationID(root, key)
	label := "com.sailfin.installation=" + installation

	dbPath := options.db
	if dbPath == "" {
		dbPath = branding.DefaultDBPath()
	}
	dbPath, err = filepath.Abs(dbPath)
	if err != nil {
		return err
	}
	if info, err := os.Lstat(dbPath); err == nil && !info.Mode().IsRegular() {
		return errors.New("SAILFIN_DB 必须指向普通文件")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	dataDir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return err
	}
	dataDir, err = filepath.EvalSymlinks(dataDir)
	if err != nil {
		return err
	}
	options.db = filepath.Join(dataDir, filepath.Base(dbPath))

	imageRef, err := loadPackagedImage(ctx, docker)
	if err != nil {
		return err
	}
	securityOptions, err := docker.output(ctx, "info", "--format", "{{json .SecurityOptions}}")
	if err != nil {
		return err
	}
	user := fmt.Sprintf("%d:%d", os.Geteuid(), os.Getegid())
	if strings.Contains(securityOptions, "name=rootless") {
		user = "0:0"
	}

	webDir := "/opt/sailfin/web"
	args := []string{"container", "run", "--detach", "--rm", "--pull=never",
		"--name", "sailfin-" + installation[:16], "--label", label,
		"--publish", address + ":8080", "--user", user,
		"--read-only", "--tmpfs", "/tmp:rw,noexec,nosuid,nodev,mode=1777", "--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--mount", dockerMount(dataDir, "/data", false),
		"--mount", dockerMount(keyPath, containerKeyPath, true),
		"--env", "SAILFIN_ADMIN_NAME", "--env", "SAILFIN_ADMIN_PASSWORD",
	}
	var webUpdateKey ed25519.PublicKey
	if path := os.Getenv(updateKeyEnv); path != "" {
		installRoot, err := filepath.EvalSymlinks("/data/sailfin")
		if err != nil || installRoot != root {
			return errors.New("网页更新要求 Sailfin 安装在 /data/sailfin")
		}
		if within(filepath.Join(root, aquaDir), dataDir) {
			return errors.New("网页更新要求 .aqua 位于容器的数据挂载之外")
		}
		resolvedKey, err := filepath.EvalSymlinks(path)
		if err != nil {
			return fmt.Errorf("读取网页更新公钥：%w", err)
		}
		resolvedKey, err = filepath.Abs(resolvedKey)
		if err != nil {
			return err
		}
		if within(resolvedKey, dataDir) {
			return errors.New("网页更新公钥必须位于容器的数据挂载之外")
		}
		webUpdateKey, err = update.PublicKey(resolvedKey)
		if err != nil {
			return fmt.Errorf("读取网页更新公钥：%w", err)
		}
		args = append(args,
			"--env", "SAILFIN_WEB_UPDATE_KEY="+base64.StdEncoding.EncodeToString(webUpdateKey),
			"--env", "SAILFIN_WEB_UPDATE_PLATFORM="+runtime.GOOS+"/"+runtime.GOARCH)
	}
	if options.webDir != "" {
		customWeb, err := filepath.Abs(options.webDir)
		if err != nil {
			return err
		}
		webDir = "/opt/sailfin/custom-web"
		args = append(args, "--mount", dockerMount(customWeb, webDir, true))
	}
	args = append(args, imageRef, "container-serve",
		"--addr", "0.0.0.0:8080", "--web-dir", webDir,
		"--db", "/data/"+filepath.Base(options.db), "--public-url", options.publicURL,
		"--offline-seconds", strconv.Itoa(options.offline))

	// Recover an instance left behind after an unclean Aqua exit. The filter
	// matches the private installation identity, never an arbitrary name.
	if err := docker.stopOwned(ctx, label); err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		runErr = errors.Join(runErr, docker.stopOwned(cleanup, label))
	}()
	containerID, err := docker.output(ctx, args...)
	if err != nil {
		return err
	}
	if !dockerDigest.MatchString(containerID) {
		return errors.New("Docker 未返回有效的 Sailfin 容器 ID")
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	logs := docker.command(ctx, "container", "logs", "--follow", containerID)
	logs.Stdout, logs.Stderr = os.Stdout, os.Stderr
	if err := logs.Start(); err == nil {
		defer func() {
			cancel()
			_ = logs.Wait()
		}()
	}

	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	containerDone := make(chan error, 1)
	go func() {
		exitCode, err := docker.output(ctx, "container", "wait", containerID)
		if err == nil {
			err = fmt.Errorf("Sailfin 容器已退出（退出码 %s）", exitCode)
		}
		containerDone <- err
	}()
	startup, cancelStartup := context.WithTimeout(ctx, 25*time.Second)
	err = waitDockerHealth(startup, client, coreURL, key, containerDone)
	cancelStartup()
	if err != nil {
		return err
	}
	return serveDockerProbe(ctx, client, coreURL, key, dataDir, options, containerDone, ready, webUpdateKey)
}

func loadPackagedImage(ctx context.Context, docker localDockerHost) (string, error) {
	raw, err := os.ReadFile("image-ref")
	var ref string
	switch {
	case err == nil:
		ref = strings.TrimSpace(string(raw))
		if !packagedImageTag.MatchString(ref) {
			return "", errors.New("安装包中的 Docker 镜像标签无效")
		}
	case errors.Is(err, os.ErrNotExist):
		// Older installations recorded the image ID instead of an archive tag.
		raw, err = os.ReadFile("image-id")
		if err != nil {
			return "", err
		}
		ref = strings.TrimSpace(string(raw))
		digest, ok := strings.CutPrefix(ref, "sha256:")
		if !ok || !dockerDigest.MatchString(digest) {
			return "", errors.New("安装包中的 Docker 镜像 ID 无效")
		}
	default:
		return "", err
	}

	if _, err := docker.output(ctx, "image", "inspect", ref); err == nil {
		return ref, nil
	}
	if _, err := docker.output(ctx, "image", "load", "--input", "image.tar"); err != nil {
		return "", err
	}
	if _, err := docker.output(ctx, "image", "inspect", ref); err != nil {
		return "", fmt.Errorf("镜像导入后无法找到 %s：%w", ref, err)
	}
	return ref, nil
}

func serveDockerProbe(ctx context.Context, client *http.Client, coreURL, key, dataDir string, options serveOptions, containerDone <-chan error, ready func(), webUpdateKey ed25519.PublicKey) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if len(webUpdateKey) != 0 {
		go watchWebUpdates(ctx, dataDir, webUpdateKey)
	}
	auto, err := newNginxAuto(dataDir, options.relayAddr)
	if err != nil {
		return err
	}
	nginx, err := hostNginxIntegration(ctx, options, auto)
	if err != nil {
		return err
	}
	managed, err := configureManagedNginx(options.nginx)
	if err != nil {
		return err
	}
	var nginxDone <-chan error
	if managed != nil {
		nginxDone, err = managed.start(ctx)
		if err != nil {
			return err
		}
		defer func() {
			cancel()
			<-nginxDone
		}()
	}

	relayReady := make(chan struct{})
	probeDone := make(chan error, 1)
	go func() {
		probeDone <- runProbe(ctx, bridgeProbeHandler(coreURL, key, client.Transport), dataDir, options, managed, auto, func(active bool) {
			auto.ready.Store(active)
			if active {
				close(relayReady)
			}
		})
		close(probeDone)
	}()
	defer func() {
		cancel()
		<-probeDone
	}()
	bridgeDone := make(chan struct{})
	go func() {
		defer close(bridgeDone)
		pollNginx(ctx, client, coreURL, key, nginx)
	}()
	defer func() {
		cancel()
		<-bridgeDone
	}()
	for {
		select {
		case <-relayReady:
			relayReady = nil
			ready()
		case err := <-probeDone:
			return err
		case err := <-nginxDone:
			return err
		case err := <-containerDone:
			if ctx.Err() == nil {
				return err
			}
			return nil
		case <-ctx.Done():
			return nil
		}
	}
}

func waitDockerHealth(ctx context.Context, client *http.Client, coreURL, key string, containerDone <-chan error) error {
	poll := time.NewTicker(250 * time.Millisecond)
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("等待 Sailfin 容器健康检查：%w", ctx.Err())
		case err := <-containerDone:
			return err
		case <-poll.C:
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, coreURL+hostBridgePath+"/api/v1/agent/config", nil)
			if err != nil {
				return err
			}
			req.Header.Set("Authorization", "Bearer "+key)
			if result, err := client.Do(req); err == nil {
				result.Body.Close()
				if result.StatusCode == http.StatusOK {
					return nil
				}
			}
		}
	}
}

type localDockerHost string

func localDocker(ctx context.Context) (localDockerHost, error) {
	endpoint := os.Getenv("DOCKER_HOST")
	if os.Getenv("DOCKER_CONTEXT") != "" || endpoint == "" {
		var err error
		args := []string{"context", "inspect", "--format", "{{.Endpoints.docker.Host}}"}
		endpoint, err = dockerOutput(exec.CommandContext(ctx, "docker", args...), args)
		if err != nil {
			return "", err
		}
	}
	if !strings.HasPrefix(endpoint, "unix:///") {
		return "", errors.New("请切换到使用本机 Unix socket 的 Docker context")
	}
	return localDockerHost(endpoint), nil
}

func dockerMount(source, target string, readOnly bool) string {
	var value strings.Builder
	writer := csv.NewWriter(&value)
	fields := []string{"type=bind", "source=" + source, "target=" + target}
	if readOnly {
		fields = append(fields, "readonly")
	}
	_ = writer.Write(fields)
	writer.Flush()
	return strings.TrimSuffix(value.String(), "\n")
}

func (docker localDockerHost) owned(ctx context.Context, label string) (id, state string, err error) {
	list := []string{"container", "ls", "--all", "--no-trunc", "--filter", "label=" + label, "--format", "{{.ID}} {{.State}}"}
	result, err := docker.output(ctx, list...)
	if err != nil || result == "" {
		return "", "", err
	}
	id, state, ok := strings.Cut(result, " ")
	if !ok || !dockerDigest.MatchString(id) || strings.Contains(state, "\n") {
		return "", "", errors.New("发现多个匹配的 Sailfin 容器，请检查当前安装")
	}
	return id, state, nil
}

func (docker localDockerHost) stopOwned(ctx context.Context, label string) error {
	id, state, err := docker.owned(ctx, label)
	if err != nil || id == "" {
		return err
	}
	switch state {
	case "created", "exited", "dead":
		_, err = docker.output(ctx, "container", "rm", id)
	default:
		_, err = docker.output(ctx, "container", "stop", "--time", "12", id)
	}
	if err != nil {
		remaining, _, listErr := docker.owned(ctx, label)
		if listErr == nil && remaining == "" {
			return nil
		}
	}
	return err
}

func (docker localDockerHost) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "docker", append([]string{"--host", string(docker)}, args...)...)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "DOCKER_CONTEXT=") && !strings.HasPrefix(entry, "DOCKER_HOST=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	return cmd
}

func (docker localDockerHost) output(ctx context.Context, args ...string) (string, error) {
	return dockerOutput(docker.command(ctx, args...), args)
}

func dockerOutput(cmd *exec.Cmd, args []string) (string, error) {
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("docker %s：%w %s", strings.Join(args[:2], " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(output)), nil
}

func installationID(root, key string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(root+"\x00"+key)))
}

func aquaOrphan(action string) error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	key, err := os.ReadFile(filepath.Join(root, aquaDir, "probe.key"))
	if errors.Is(err, os.ErrNotExist) {
		fmt.Println("Sailfin 未运行")
		return nil
	}
	if err != nil {
		return err
	}
	if !validHostKey(string(key)) {
		return errors.New("无效的 Sailfin 探针密钥")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	docker, err := localDocker(ctx)
	if err != nil {
		return err
	}
	label := "com.sailfin.installation=" + installationID(root, string(key))
	if action == "stop" {
		if err := docker.stopOwned(ctx, label); err != nil {
			return err
		}
		fmt.Println("Sailfin 容器和探针已停止")
		return nil
	}
	id, state, err := docker.owned(ctx, label)
	if err != nil {
		return err
	}
	if id == "" {
		fmt.Println("Sailfin 未运行")
	} else {
		fmt.Printf("Sailfin 容器状态：%s；宿主机探针未运行\n", state)
	}
	return nil
}

func validHostKey(key string) bool {
	bytes, err := base64.RawURLEncoding.DecodeString(key)
	return err == nil && len(bytes) == 32
}

func loadHostKey(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err == nil {
		if !validHostKey(string(raw)) {
			return "", errors.New("无效的 Sailfin 探针密钥")
		}
		return string(raw), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	key := core.Random()
	temp, err := os.CreateTemp(filepath.Dir(path), "probe-key-")
	if err != nil {
		return "", err
	}
	defer os.Remove(temp.Name())
	_, writeErr := temp.WriteString(key)
	if err := errors.Join(writeErr, temp.Close()); err != nil {
		return "", err
	}
	return key, os.Rename(temp.Name(), path)
}
