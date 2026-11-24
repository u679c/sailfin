package core

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"sailfin/internal/update"
)

func (s *Server) uploadUpdate(w http.ResponseWriter, r *http.Request) error {
	if len(s.cfg.WebUpdateKey) == 0 {
		return fail(409, "宿主机尚未配置网页更新公钥")
	}
	if !s.updateMu.TryLock() {
		return fail(409, "已有更新包正在上传")
	}
	defer s.updateMu.Unlock()
	dir := update.Dir(s.cfg.DBPath)
	active := filepath.Join(dir, "active")
	if _, err := os.Stat(active); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return err
		}
		return fail(409, "已有更新正在执行")
	}
	multipart, err := r.MultipartReader()
	if err != nil {
		return fail(400, "更新包必须使用表单上传")
	}
	password, err := multipart.NextPart()
	if err != nil || password.FormName() != "password" {
		return fail(400, "缺少管理员密码")
	}
	value, err := io.ReadAll(io.LimitReader(password, 257))
	if err != nil {
		return fail(400, "无法读取管理员密码")
	}
	if err := s.checkUpdatePassword(r, string(value)); err != nil {
		return err
	}
	part, err := multipart.NextPart()
	if err != nil || part.FormName() != "package" || !update.PackageName.MatchString(part.FileName()) {
		return fail(400, "请选择有效的 Sailfin 更新包")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(dir, "upload-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	pkg, err := os.OpenFile(filepath.Join(stage, "package.bin"), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer pkg.Close()
	size, err := io.Copy(pkg, io.LimitReader(part, update.MaxPackage+1))
	if err != nil {
		return fail(400, "更新包上传中断")
	}
	if size > update.MaxPackage {
		return fail(413, "更新包不能超过 256 MiB")
	}
	if _, err := pkg.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := update.Verify(pkg, s.cfg.WebUpdateKey, s.cfg.WebUpdatePlatform); err != nil {
		return fail(400, err.Error())
	}
	if err := pkg.Sync(); err != nil {
		return err
	}
	job := update.Job{ID: filepath.Base(stage), Name: part.FileName(), Status: "started", Time: Now()}
	if err := update.WriteJob(filepath.Join(stage, "job.json"), job); err != nil {
		return err
	}
	if err := os.Rename(stage, active); err != nil {
		return err
	}
	s.audit("system.update.started", job.ID)
	jsonOut(w, http.StatusAccepted, job)
	return nil
}

func (s *Server) webUpdateStatus(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	if !update.ID.MatchString(id) {
		return fail(404, "更新任务不存在")
	}
	dir := update.Dir(s.cfg.DBPath)
	job, err := update.ReadJob(filepath.Join(dir, id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		job, err = update.ReadJob(filepath.Join(dir, "active", "job.json"))
	}
	if errors.Is(err, os.ErrNotExist) || (err == nil && job.ID != id) {
		return fail(404, "更新任务不存在")
	}
	if err != nil {
		return err
	}
	jsonOut(w, 200, job)
	return nil
}

func (s *Server) webUpdateHistory(w http.ResponseWriter, r *http.Request) error {
	if err := s.importWebUpdates(r.Context()); err != nil {
		return err
	}
	return s.pagedQuery(w, r, `WITH job_events AS (
		SELECT target, action,
			min(created_at) OVER (PARTITION BY target) AS started_at,
			row_number() OVER (PARTITION BY target
				ORDER BY (action='system.update.started'), created_at DESC, id DESC) AS latest
		FROM audit_logs
		WHERE action IN ('system.update.started', 'system.update.succeeded', 'system.update.failed', 'system.update.rolled_back')
	)
	SELECT e.target AS id, e.started_at AS created_at, p.name AS package_name, e.action
	FROM job_events e LEFT JOIN web_update_packages p ON p.job_id=e.target
	WHERE e.latest=1 ORDER BY e.started_at DESC, e.target DESC LIMIT ? OFFSET ?`)
}

func (s *Server) rememberUpdatePackage(ctx context.Context, job update.Job) error {
	if !update.ID.MatchString(job.ID) || !update.PackageName.MatchString(job.Name) {
		return nil
	}
	_, err := s.DB.ExecContext(ctx, "INSERT OR IGNORE INTO web_update_packages(job_id,name) VALUES(?,?)", job.ID, job.Name)
	return err
}

func (s *Server) importWebUpdates(ctx context.Context) error {
	dir := update.Dir(s.cfg.DBPath)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	active, err := update.ReadJob(filepath.Join(dir, "active", "job.json"))
	if err == nil {
		if err := s.rememberUpdatePackage(ctx, active); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		id, ok := cutJobResult(name)
		if !ok || !entry.Type().IsRegular() {
			continue
		}
		job, err := update.ReadJob(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if job.ID != id || (job.Status != "succeeded" && job.Status != "failed") {
			continue
		}
		if err := s.rememberUpdatePackage(ctx, job); err != nil {
			return err
		}
		action := "system.update." + job.Status
		if _, err := s.DB.ExecContext(ctx, `INSERT INTO audit_logs(action,target,created_at)
			SELECT ?,?,? WHERE NOT EXISTS (SELECT 1 FROM audit_logs WHERE action=? AND target=?)`,
			action, job.ID, job.Time, action, job.ID); err != nil {
			return err
		}
	}
	return nil
}

func cutJobResult(name string) (string, bool) {
	if filepath.Ext(name) != ".json" {
		return "", false
	}
	id := name[:len(name)-len(".json")]
	return id, update.ID.MatchString(id)
}
