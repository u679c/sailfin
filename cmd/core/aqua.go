package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const (
	aquaDir     = ".aqua"
	aquaSocket  = aquaDir + "/control.sock"
	aquaLog     = aquaDir + "/sailfin.log"
	aquaStartup = aquaDir + "/startup.json"
	aquaTimeout = 2 * time.Minute
)

func aquaCommand(args []string) error {
	usage := errors.New("用法：./aqua start [serve 参数] | stop | restart | status")
	if len(args) == 0 {
		return usage
	}
	action := args[0]
	switch action {
	case "start":
		if _, err := serveFlags(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
	case "stop", "restart", "status":
		if len(args) != 1 {
			return usage
		}
	default:
		return usage
	}

	lock, err := lockAqua()
	if err != nil {
		return err
	}
	defer lock.Close()

	conn, err := net.DialTimeout("unix", aquaSocket, time.Second)
	if err == nil {
		return aquaRequest(conn, action)
	}
	if !errors.Is(err, syscall.ENOENT) && !errors.Is(err, syscall.ECONNREFUSED) {
		return fmt.Errorf("无法连接 aqua：%w", err)
	}
	if action == "status" || action == "stop" {
		return aquaOrphan(action)
	}
	// After an unclean exit only remove the stale socket, never a regular file.
	stale, err := os.Lstat(aquaSocket)
	if err == nil {
		if stale.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("控制路径被占用：%s", aquaSocket)
		}
		if err := os.Remove(aquaSocket); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	startArgs := args[1:]
	if action == "restart" {
		startArgs, err = restoreAquaStartup()
		if err != nil {
			return err
		}
	}
	return aquaStart(startArgs)
}

func lockAqua() (*os.File, error) {
	if err := os.Mkdir(aquaDir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	info, err := os.Lstat(aquaDir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return nil, fmt.Errorf("%s 必须是仅当前用户可访问的目录", aquaDir)
	}
	lock, err := os.OpenFile(aquaDir+"/command.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.New("已有 aqua 命令正在执行")
		}
		return nil, err
	}
	return lock, nil
}

var startupEnv = []string{
	updateKeyEnv,
	"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG", "SAILFIN_ADDR", "SAILFIN_DB",
	"SAILFIN_PUBLIC_URL", "SAILFIN_WEB_DIR", "SAILFIN_SITES_FILE", "SAILFIN_RELAY_ADDR",
	"SAILFIN_PROBE_STATE", "SAILFIN_LOG_ROOTS", "SAILFIN_OPENRESTY_CONTAINER",
	"SAILFIN_OFFLINE_SECONDS", "SAILFIN_NGINX_UPSTREAM", "SAILFIN_NGINX_PORT",
	"SAILFIN_NGINX_SITE_ID", "SAILFIN_NGINX_SITE_NAME", "SAILFIN_NGINX_REQUIRE_CONSENT",
}

type aquaStartupConfig struct {
	Args []string
	Env  map[string]string
}

func saveAquaStartup(args []string) error {
	config := aquaStartupConfig{Args: args, Env: make(map[string]string)}
	for _, key := range startupEnv {
		if value, set := os.LookupEnv(key); set {
			config.Env[key] = value
		}
	}
	file, err := os.CreateTemp(aquaDir, "startup-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := errors.Join(json.NewEncoder(file).Encode(config), file.Close()); err != nil {
		return err
	}
	return os.Rename(file.Name(), aquaStartup)
}

func restoreAquaStartup() ([]string, error) {
	data, err := os.ReadFile(aquaStartup)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var config aquaStartupConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	for _, key := range startupEnv {
		if value, set := config.Env[key]; set {
			err = os.Setenv(key, value)
		} else {
			err = os.Unsetenv(key)
		}
		if err != nil {
			return nil, err
		}
	}
	return config.Args, nil
}

func aquaRequest(conn net.Conn, action string) error {
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(aquaTimeout)); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(conn, action); err != nil {
		return err
	}
	body, err := io.ReadAll(io.LimitReader(conn, 4096))
	if err != nil {
		return err
	}
	response := strings.TrimSpace(string(body))
	if message, ok := strings.CutPrefix(response, "ok "); ok {
		fmt.Println(message)
		return nil
	}
	if message, ok := strings.CutPrefix(response, "error "); ok {
		return errors.New(message)
	}
	return fmt.Errorf("aqua 未返回操作结果，请查看 %s", aquaLog)
}

func aquaStart(args []string) error {
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	log, err := os.OpenFile(aquaLog, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := exec.Command(binary, append([]string{"aqua-serve"}, args...)...)
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	poll := time.NewTicker(100 * time.Millisecond)
	defer poll.Stop()
	timeout := time.NewTimer(aquaTimeout)
	defer timeout.Stop()
	for {
		select {
		case err := <-done:
			return fmt.Errorf("Sailfin 启动失败，请查看 %s（%v）", aquaLog, err)
		case <-timeout.C:
			_ = cmd.Process.Signal(syscall.SIGTERM)
			return fmt.Errorf("Sailfin 启动超时，请查看 %s", aquaLog)
		case <-poll.C:
			conn, err := net.DialTimeout("unix", aquaSocket, time.Second)
			if err != nil {
				continue
			}
			if err := aquaRequest(conn, "status"); err != nil {
				_ = cmd.Process.Signal(syscall.SIGTERM)
				return fmt.Errorf("Sailfin 启动失败，请查看 %s：%w", aquaLog, err)
			}
			if err := saveAquaStartup(args); err != nil {
				fmt.Fprintf(os.Stderr, "无法保存 Aqua 启动配置：%v\n", err)
			}
			return nil
		}
	}
}

// The private Unix socket identifies the instance by installation directory and
// avoids signalling a PID that the OS may have reassigned after a crash.
func aquaServe(args []string) error {
	options, err := serveFlags(args)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	listener, err := net.Listen("unix", aquaSocket)
	if err != nil {
		return err
	}
	defer listener.Close()

	current, err := startAquaCore(ctx, options)
	if err != nil {
		return err
	}
	defer func() { current.stop() }()

	connections := make(chan net.Conn)
	go func() {
		defer close(connections)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			select {
			case connections <- conn:
			case <-ctx.Done():
				conn.Close()
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			current.stop()
			return current.err
		case <-current.done:
			return current.err
		case conn, ok := <-connections:
			if !ok {
				return errors.New("aqua 控制连接已关闭")
			}
			action := readAquaAction(conn)
			var message string
			switch action {
			case "start", "status":
				message = fmt.Sprintf("ok Sailfin Docker 容器和宿主机探针运行中（探针进程 PID %d）", os.Getpid())
			case "stop":
				current.stop()
				_ = listener.Close()
				if current.err != nil {
					fmt.Fprintln(conn, "error Sailfin 停止失败：", current.err)
				} else {
					fmt.Fprintln(conn, "ok Sailfin 容器和探针已停止")
				}
				conn.Close()
				return current.err
			case "restart":
				current.stop()
				next, err := startAquaCore(ctx, options)
				if err != nil {
					_ = listener.Close()
					fmt.Fprintf(conn, "error Sailfin 重启失败，请查看 %s：%v\n", aquaLog, err)
					conn.Close()
					return err
				}
				current = next
				message = "ok Sailfin 已重启"
			default:
				message = "error 无法识别 aqua 命令"
			}
			fmt.Fprintln(conn, message)
			conn.Close()
		}
	}
}

func readAquaAction(conn net.Conn) string {
	_ = conn.SetDeadline(time.Now().Add(aquaTimeout))
	scanner := bufio.NewScanner(io.LimitReader(conn, 32))
	if scanner.Scan() {
		return scanner.Text()
	}
	return ""
}

type aquaCore struct {
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

func startAquaCore(ctx context.Context, options serveOptions) (*aquaCore, error) {
	ctx, cancel := context.WithCancel(ctx)
	service := &aquaCore{cancel: cancel, done: make(chan struct{})}
	ready := make(chan struct{})
	go func() {
		defer close(service.done)
		service.err = runDocker(ctx, options, func() { close(ready) })
	}()
	select {
	case <-ready:
		return service, nil
	case <-service.done:
		cancel()
		if service.err != nil {
			return nil, service.err
		}
		return nil, errors.New("Sailfin 在启动完成前退出")
	}
}

func (service *aquaCore) stop() {
	service.cancel()
	<-service.done
}
