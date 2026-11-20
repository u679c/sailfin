package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type nginxInstance struct {
	pid     int
	args    []string
	pidFile string
	files   []string
}

var nginxMasterLine = regexp.MustCompile(`^\s*(\d+)\s+(\d+)\s+nginx: master process (.*)$`)
var nginxStartupFlag = regexp.MustCompile(`\s+-([cpeg])\s+`)
var nginxServiceFlags = regexp.MustCompile(`^(\s*(daemon|master_process)\s+(on|off);\s*)+$`)

func runningNginx(ctx context.Context) (string, []nginxInstance, []string, error) {
	binary, err := exec.LookPath("nginx")
	if err != nil {
		return "", nil, nil, fmt.Errorf("宿主机未找到 nginx 命令。可在 sites.json 中配置访问日志路径，并授权对应目录")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "ps", "-Aww", "-o", "uid=,pid=,command=").Output()
	if err != nil {
		return "", nil, nil, fmt.Errorf("无法读取本机 Nginx 进程: %w", err)
	}
	var instances []nginxInstance
	var warnings []string
	for _, line := range strings.Split(string(output), "\n") {
		entry := nginxMasterLine.FindStringSubmatch(line)
		if entry == nil {
			continue
		}
		uid, _ := strconv.Atoi(entry[1])
		pid, _ := strconv.Atoi(entry[2])
		if uid != os.Geteuid() {
			warnings = append(warnings, fmt.Sprintf("Nginx PID %d 与 Core 的运行用户不同，当前没有重载权限", pid))
			continue
		}
		args, err := nginxMasterArgs(entry[3])
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("Nginx PID %d: %v", pid, err))
			continue
		}
		instances = append(instances, nginxInstance{pid: pid, args: args})
	}
	return binary, instances, warnings, nil
}

// Nginx's process title joins argv with spaces. A path may contain spaces; only
// supported option boundaries split it. Global flags are strictly allowlisted.
func nginxMasterArgs(title string) ([]string, error) {
	options := nginxStartupFlag.FindAllStringSubmatchIndex(title, -1)
	end := len(title)
	if len(options) != 0 {
		end = options[0][0]
	}
	if name := strings.TrimSpace(title[:end]); filepath.Base(name) != "nginx" {
		return nil, fmt.Errorf("无法识别启动参数")
	}
	args := []string{"-e", "stderr"}
	for i, option := range options {
		end = len(title)
		if i+1 < len(options) {
			end = options[i+1][0]
		}
		name := title[option[2]:option[3]]
		value := strings.TrimSpace(title[option[1]:end])
		// Ignore the master's error-log option: validation writes to stderr.
		if name == "e" {
			continue
		}
		if name == "g" {
			if !nginxServiceFlags.MatchString(value) {
				return nil, fmt.Errorf("包含未支持的 Nginx 全局启动参数")
			}
		} else if !filepath.IsAbs(value) {
			return nil, fmt.Errorf("使用了相对路径启动参数")
		}
		args = append(args, "-"+name, value)
	}
	return args, nil
}

func inspectNginx(ctx context.Context, binary string, instance *nginxInstance, dump []byte) error {
	var firstFile string
	seen := map[string]bool{}
	for _, line := range strings.Split(string(dump), "\n") {
		path, ok := strings.CutPrefix(line, "# configuration file ")
		if !ok || !strings.HasSuffix(path, ":") {
			continue
		}
		path, err := filepath.EvalSymlinks(strings.TrimSuffix(path, ":"))
		if err != nil || !filepath.IsAbs(path) || seen[path] {
			continue
		}
		if firstFile == "" {
			firstFile = path
		}
		seen[path] = true
		instance.files = append(instance.files, path)
	}
	if firstFile == "" {
		return fmt.Errorf("未发现加载的配置文件")
	}
	main, err := readDeploymentFile(firstFile, false)
	if err != nil {
		return err
	}
	nodes, err := parseNginx(string(main.before))
	if err != nil {
		return err
	}
	for _, node := range nginxDirective(nodes, "pid") {
		if len(node.args) == 2 {
			instance.pidFile = node.args[1]
		}
	}
	if !filepath.IsAbs(instance.pidFile) {
		if err := resolveNginxPIDFile(ctx, binary, instance); err != nil {
			return err
		}
	}
	return verifyNginxMaster(*instance)
}

func resolveNginxPIDFile(ctx context.Context, binary string, instance *nginxInstance) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	version, err := exec.CommandContext(ctx, binary, "-V").CombinedOutput()
	if err != nil {
		return fmt.Errorf("无法读取 Nginx 编译参数")
	}
	prefix, pid := "/usr/local/nginx", "logs/nginx.pid"
	for _, arg := range strings.Fields(string(version)) {
		switch {
		case strings.HasPrefix(arg, "--prefix="):
			prefix = strings.Trim(strings.TrimPrefix(arg, "--prefix="), "'\"")
		case strings.HasPrefix(arg, "--pid-path="):
			pid = strings.Trim(strings.TrimPrefix(arg, "--pid-path="), "'\"")
		}
	}
	for i, arg := range instance.args {
		if arg == "-p" {
			prefix = instance.args[i+1]
		}
	}
	if instance.pidFile == "" {
		instance.pidFile = pid
	}
	if !filepath.IsAbs(instance.pidFile) {
		instance.pidFile = filepath.Join(prefix, instance.pidFile)
	}
	return nil
}

func verifyNginxMaster(instance nginxInstance) error {
	bytes, err := os.ReadFile(instance.pidFile)
	if err != nil || strings.TrimSpace(string(bytes)) != strconv.Itoa(instance.pid) ||
		syscall.Kill(instance.pid, 0) != nil {
		return fmt.Errorf("无法确认配置属于运行中的 Nginx PID %d", instance.pid)
	}
	return nil
}
