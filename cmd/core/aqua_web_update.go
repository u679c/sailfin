package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"sailfin/internal/update"
)

const updateKeyEnv = "SAILFIN_UPDATE_PUBLIC_KEY"

func watchWebUpdates(ctx context.Context, dataDir string, key ed25519.PublicKey) {
	dir := update.Dir(filepath.Join(dataDir, "sailfin.db"))
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := launchWebUpdate(dir, key); err != nil {
				slog.Error("无法启动网页更新", "error", err)
			}
		}
	}
}

func launchWebUpdate(dir string, key ed25519.PublicKey) error {
	active := filepath.Join(dir, "active")
	claim := filepath.Join(active, "running")
	lock, err := os.OpenFile(claim, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	lock.Close()
	binary, err := os.Executable()
	if err != nil {
		_ = os.Remove(claim)
		return err
	}
	cmd := exec.Command(binary, "aqua-apply-web-update", dir, base64.StdEncoding.EncodeToString(key))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		_ = os.Remove(claim)
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func applyWebUpdate(dir string, key ed25519.PublicKey) error {
	active := filepath.Join(dir, "active")
	job, err := update.ReadJob(filepath.Join(active, "job.json"))
	if err != nil {
		return err
	}
	if !update.ID.MatchString(job.ID) || !update.PackageName.MatchString(job.Name) {
		return errors.New("invalid update job")
	}
	err = executeWebUpdate(filepath.Join(active, "package.bin"), key)
	job.Status = "succeeded"
	job.Time = time.Now().UnixMilli()
	if err != nil {
		job.Status = "failed"
		job.Message = "更新失败，请查看宿主机 .aqua/sailfin.log"
	}
	if writeErr := update.WriteJob(filepath.Join(dir, job.ID+".json"), job); writeErr != nil {
		return errors.Join(err, writeErr)
	}
	return errors.Join(err, os.RemoveAll(active))
}

func executeWebUpdate(path string, key ed25519.PublicKey) error {
	pkg, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer pkg.Close()
	if info, err := pkg.Stat(); err != nil || !info.Mode().IsRegular() {
		return errors.New("invalid staged update package")
	}
	// Verify and execute the same private copy, outside the container's writable mounts.
	file, err := os.CreateTemp(aquaDir, "web-package-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	size, err := io.Copy(file, io.LimitReader(pkg, update.MaxPackage+1))
	if err != nil || size > update.MaxPackage {
		return errors.New("cannot stage web update package")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := update.Verify(file, key, runtime.GOOS+"/"+runtime.GOARCH); err != nil {
		return err
	}
	for _, option := range []string{"--check", "--apply"} {
		cmd := exec.Command("/bin/bash", file.Name(), option)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("web update %s: %w", option, err)
		}
	}
	return nil
}
