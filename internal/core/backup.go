package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type backupSchedule struct {
	Enabled       bool `json:"enabled"`
	IntervalHours int  `json:"intervalHours"`
	Keep          int  `json:"keep"`
}

type backupRun struct {
	LastAttempt int64  `json:"lastAttempt"`
	LastSuccess int64  `json:"lastSuccess"`
	LastError   string `json:"lastError"`
}

type backupFile struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	CreatedAt int64  `json:"createdAt"`
	Bytes     int64  `json:"bytes"`
}

const backupFilenameTime = "20060102T150405.000"

var managedBackupName = regexp.MustCompile(`^sailfin-(auto|manual)-[0-9]{8}T[0-9]{6}\.[0-9]{3}-[A-Za-z0-9_-]{12}\.db$`)

type settingsReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readJSONSetting(ctx context.Context, db settingsReader, name string, target any) error {
	var raw string
	err := db.QueryRowContext(ctx, "SELECT value FROM system_settings WHERE name=?", name).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), target)
}

func (s *Server) writeJSONSetting(ctx context.Context, name string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO system_settings(name,value) VALUES(?,?)
		ON CONFLICT(name) DO UPDATE SET value=excluded.value`, name, string(raw))
	return err
}

func (s *Server) loadBackupSchedule(ctx context.Context) (backupSchedule, error) {
	schedule := backupSchedule{IntervalHours: 24, Keep: 7}
	err := readJSONSetting(ctx, s.DB, "backup.schedule", &schedule)
	return schedule, err
}

func (s *Server) getBackupSchedule(w http.ResponseWriter, r *http.Request) error {
	schedule, err := s.loadBackupSchedule(r.Context())
	if err != nil {
		return err
	}
	var run backupRun
	if err := readJSONSetting(r.Context(), s.DB, "backup.run", &run); err != nil {
		return err
	}
	jsonOut(w, 200, struct {
		backupSchedule
		backupRun
	}{schedule, run})
	return nil
}

func (s *Server) setBackupSchedule(w http.ResponseWriter, r *http.Request) error {
	var schedule backupSchedule
	if err := decode(r, &schedule); err != nil {
		return err
	}
	if schedule.IntervalHours < 1 || schedule.IntervalHours > 720 || schedule.Keep < 1 || schedule.Keep > 30 {
		return fail(400, "备份间隔须为 1–720 小时，保留份数须为 1–30")
	}
	if err := s.writeJSONSetting(r.Context(), "backup.schedule", schedule); err != nil {
		return err
	}
	s.audit("database.backup.schedule", fmt.Sprintf("enabled=%t hours=%d keep=%d", schedule.Enabled, schedule.IntervalHours, schedule.Keep))
	jsonOut(w, 200, schedule)
	return nil
}

func (s *Server) backupDir() string {
	return filepath.Join(filepath.Dir(s.cfg.DBPath), "backups", filepath.Base(s.cfg.DBPath))
}

func (s *Server) backupStage() (string, error) {
	dir := s.backupDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return os.MkdirTemp(dir, ".backup-")
}

func (s *Server) snapshot(ctx context.Context, path string) error {
	if _, err := s.DB.ExecContext(ctx, "VACUUM INTO ?", path); err != nil {
		return err
	}
	return os.Chmod(path, 0600)
}

func (s *Server) createBackup(ctx context.Context, kind string) (backupFile, error) {
	stage, err := s.backupStage()
	if err != nil {
		return backupFile{}, err
	}
	defer os.RemoveAll(stage)
	staged := filepath.Join(stage, "snapshot.db")
	if err := s.snapshot(ctx, staged); err != nil {
		return backupFile{}, err
	}
	info, err := os.Stat(staged)
	if err != nil {
		return backupFile{}, err
	}
	now := time.Now().UTC()
	name := "sailfin-" + kind + "-" + now.Format(backupFilenameTime) + "-" + Random()[:12] + ".db"
	if err := os.Rename(staged, filepath.Join(s.backupDir(), name)); err != nil {
		return backupFile{}, err
	}
	return backupFile{Name: name, Kind: kind, CreatedAt: now.UnixMilli(), Bytes: info.Size()}, nil
}

func (s *Server) listBackups() ([]backupFile, error) {
	files := []backupFile{}
	dir := s.backupDir()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return files, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		parts := managedBackupName.FindStringSubmatchIndex(entry.Name())
		if parts == nil || !entry.Type().IsRegular() {
			continue
		}
		name := entry.Name()
		kind := name[parts[2]:parts[3]]
		start := parts[3] + 1
		createdAt, err := time.Parse(backupFilenameTime, name[start:start+len(backupFilenameTime)])
		if err != nil {
			continue
		}
		info, err := entry.Info()
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		files = append(files, backupFile{Name: name, Kind: kind, CreatedAt: createdAt.UnixMilli(), Bytes: info.Size()})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].CreatedAt == files[j].CreatedAt {
			return files[i].Name > files[j].Name
		}
		return files[i].CreatedAt > files[j].CreatedAt
	})
	return files, nil
}

func (s *Server) managedBackups(w http.ResponseWriter, _ *http.Request) error {
	files, err := s.listBackups()
	if err != nil {
		return err
	}
	jsonOut(w, 200, files)
	return nil
}

func (s *Server) saveManualBackup(w http.ResponseWriter, r *http.Request) error {
	if !s.backupMu.TryLock() {
		return fail(409, "正在创建备份，请稍后重试")
	}
	defer s.backupMu.Unlock()
	file, err := s.createBackup(r.Context(), "manual")
	if err != nil {
		return err
	}
	s.audit("database.backup", file.Name)
	jsonOut(w, http.StatusCreated, file)
	return nil
}

func serveBackupFile(w http.ResponseWriter, r *http.Request, path, name string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) || (err == nil && !info.Mode().IsRegular()) {
		return fail(404, "备份文件不存在")
	}
	if err != nil {
		return err
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return fail(404, "备份文件不存在")
	}
	if err != nil {
		return err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(info, opened) {
		return fail(404, "备份文件不存在")
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	http.ServeContent(w, r, name, info.ModTime(), file)
	return nil
}

func (s *Server) downloadManagedBackup(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	if !managedBackupName.MatchString(name) {
		return fail(404, "备份文件不存在")
	}
	return serveBackupFile(w, r, filepath.Join(s.backupDir(), name), name)
}

func (s *Server) deleteManagedBackup(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	if !managedBackupName.MatchString(name) {
		return fail(404, "备份文件不存在")
	}
	path := filepath.Join(s.backupDir(), name)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) || (err == nil && !info.Mode().IsRegular()) {
		return fail(404, "备份文件不存在")
	}
	if err != nil {
		return err
	}
	if err := os.Remove(path); errors.Is(err, os.ErrNotExist) {
		return fail(404, "备份文件不存在")
	} else if err != nil {
		return err
	}
	s.audit("database.backup.delete", name)
	jsonOut(w, 200, map[string]bool{"ok": true})
	return nil
}

func (s *Server) backup(w http.ResponseWriter, r *http.Request) error {
	if !s.backupMu.TryLock() {
		return fail(409, "正在创建备份，请稍后重试")
	}
	dir, err := s.backupStage()
	if err != nil {
		s.backupMu.Unlock()
		return err
	}
	defer os.RemoveAll(dir)
	name := "sailfin-" + time.Now().UTC().Format("20060102T150405") + "-" + Random()[:6] + ".db"
	path := filepath.Join(dir, name)
	err = s.snapshot(r.Context(), path)
	s.backupMu.Unlock()
	if err != nil {
		return err
	}
	s.audit("database.backup", name)
	return serveBackupFile(w, r, path, name)
}

func (s *Server) pruneAutoBackups(keep int) error {
	files, err := s.listBackups()
	if err != nil {
		return err
	}
	for _, file := range files {
		if file.Kind != "auto" {
			continue
		}
		if keep > 0 {
			keep--
			continue
		}
		if err := os.Remove(filepath.Join(s.backupDir(), file.Name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (s *Server) runAutoBackup(ctx context.Context) error {
	schedule, err := s.loadBackupSchedule(ctx)
	if err != nil || !schedule.Enabled {
		return err
	}
	var run backupRun
	if err := readJSONSetting(ctx, s.DB, "backup.run", &run); err != nil {
		return err
	}
	now := Now()
	if now-run.LastSuccess < (time.Duration(schedule.IntervalHours)*time.Hour).Milliseconds() ||
		now-run.LastAttempt < (5*time.Minute).Milliseconds() || !s.backupMu.TryLock() {
		return nil
	}
	defer s.backupMu.Unlock()
	run.LastAttempt = now
	file, err := s.createBackup(ctx, "auto")
	run.LastError = ""
	if err != nil {
		run.LastError = "自动备份失败，请查看 Core 日志。"
	} else {
		run.LastSuccess = file.CreatedAt
		s.audit("database.backup.auto", file.Name)
		if pruneErr := s.pruneAutoBackups(schedule.Keep); pruneErr != nil {
			run.LastError = "已创建备份，清理旧备份失败，请查看 Core 日志。"
			err = pruneErr
		}
	}
	return errors.Join(err, s.writeJSONSetting(ctx, "backup.run", run))
}

func (s *Server) Maintain(ctx context.Context) {
	backupTick := time.NewTicker(time.Minute)
	cleanupTick := time.NewTicker(time.Hour)
	defer backupTick.Stop()
	defer cleanupTick.Stop()
	runBackup := func() {
		if err := s.runAutoBackup(ctx); err != nil && ctx.Err() == nil {
			slog.Error("Automatic database backup failed", "error", err)
		}
	}
	runBackup()
	for {
		select {
		case <-ctx.Done():
			return
		case <-backupTick.C:
			runBackup()
		case <-cleanupTick.C:
			runBackup()
			if err := s.cleanup(ctx); err != nil && ctx.Err() == nil {
				slog.Error("Database cleanup failed", "error", err)
			}
		}
	}
}

func isBackupTransfer(r *http.Request) bool {
	return (r.Method == http.MethodPost && (r.URL.Path == "/api/v1/system/backup" || r.URL.Path == "/api/v1/system/backups")) ||
		((r.Method == http.MethodGet || r.Method == http.MethodHead) && strings.HasPrefix(r.URL.Path, "/api/v1/system/backups/"))
}
