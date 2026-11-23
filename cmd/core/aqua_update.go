package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func replaceAquaRelease(stage string) error {
	current, err := os.Stat(".")
	if err != nil {
		return err
	}
	installed, err := os.Lstat("/data/sailfin")
	if err != nil || !os.SameFile(current, installed) || !installed.IsDir() {
		return errors.New("只能在 /data/sailfin 更新 Sailfin")
	}
	bin, err := os.Lstat("bin")
	if err != nil || !bin.IsDir() {
		return errors.New("安装目录缺少有效的 bin 目录")
	}
	lock, err := lockAqua()
	if err != nil {
		return err
	}
	defer lock.Close()
	if _, err := os.Lstat(aquaStartup); errors.Is(err, os.ErrNotExist) && os.Getenv("SAILFIN_DB") == "" {
		return errors.New("缺少历史启动配置，请沿用原启动环境并显式设置 SAILFIN_DB 后再更新")
	}
	if _, err := restoreAquaStartup(); err != nil {
		return fmt.Errorf("读取 Aqua 启动配置：%w", err)
	}

	files := []string{"aqua", "bin/sailfin-core", "image.tar", "image-ref"}
	for _, name := range files {
		info, err := os.Lstat(filepath.Join(stage, name))
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("更新包缺少有效文件：%s", name)
		}
		if (name == "aqua" || name == "bin/sailfin-core") && info.Mode().Perm()&0100 == 0 {
			return fmt.Errorf("更新包中的文件不可执行：%s", name)
		}
	}
	refBytes, err := os.ReadFile(filepath.Join(stage, "image-ref"))
	if err != nil {
		return err
	}
	ref := strings.TrimSpace(string(refBytes))
	if !packagedImageTag.MatchString(ref) {
		return errors.New("更新包中的镜像标签无效")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	docker, err := localDocker(ctx)
	if err != nil {
		return err
	}
	fmt.Println("导入新版本镜像")
	if _, err := docker.output(ctx, "image", "load", "--input", filepath.Join(stage, "image.tar")); err != nil {
		return err
	}
	if _, err := docker.output(ctx, "image", "inspect", ref); err != nil {
		return err
	}

	backup, err := os.MkdirTemp(aquaDir, "previous-release-")
	if err != nil {
		return err
	}
	if err := os.Mkdir(filepath.Join(backup, "bin"), 0700); err != nil {
		return err
	}
	saved := make(map[string]bool)
	for _, name := range files {
		info, err := os.Lstat(name)
		if name == "image-ref" && errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("无法备份安装文件：%s", name)
		}
		if err := os.Link(name, filepath.Join(backup, name)); err != nil {
			return fmt.Errorf("备份 %s：%w", name, err)
		}
		saved[name] = true
	}
	fmt.Printf("上一版本文件保存在 %s\n", backup)

	conn, err := net.DialTimeout("unix", aquaSocket, time.Second)
	switch {
	case err == nil:
		err = aquaRequest(conn, "stop")
	case errors.Is(err, syscall.ENOENT), errors.Is(err, syscall.ECONNREFUSED):
		err = aquaOrphan("stop")
	}
	if err != nil {
		return err
	}

	for i, name := range files {
		if err := os.Rename(filepath.Join(stage, name), name); err != nil {
			result := fmt.Errorf("替换 %s：%w", name, err)
			for _, replaced := range files[:i] {
				if saved[replaced] {
					result = errors.Join(result, os.Rename(filepath.Join(backup, replaced), replaced))
				} else {
					result = errors.Join(result, os.Remove(replaced))
				}
			}
			return result
		}
	}
	return nil
}
