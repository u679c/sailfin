package core

import (
	"net/http"
	"syscall"
)

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		Name string `json:"confirmName"`
	}
	if e := decode(r, &b); e != nil {
		return e
	}
	id := r.PathValue("id")
	tx, e := s.DB.BeginTx(r.Context(), nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var name string
	if e = tx.QueryRow("SELECT name FROM projects WHERE id=?", id).Scan(&name); e != nil {
		return fail(404, "project not found")
	}
	if b.Name != name {
		return fail(400, "project name confirmation does not match")
	}
	queries := []string{"DELETE FROM agent_configs WHERE agent_id IN (SELECT id FROM agents WHERE project_id=?)", "DELETE FROM agent_logs WHERE agent_id IN (SELECT id FROM agents WHERE project_id=?)", "DELETE FROM agents WHERE project_id=?", "DELETE FROM enrollments WHERE project_id=?", "DELETE FROM events WHERE project_id=?", "DELETE FROM daily_usage WHERE project_id=?", "DELETE FROM project_configs WHERE project_id=?", "DELETE FROM projects WHERE id=?"}
	for _, q := range queries {
		if _, e = tx.Exec(q, id); e != nil {
			return e
		}
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	s.audit("project.delete", id)
	jsonOut(w, 200, map[string]bool{"ok": true})
	return nil
}
func (s *Server) deleteAgent(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		Name string `json:"confirmName"`
	}
	if e := decode(r, &b); e != nil {
		return e
	}
	id := r.PathValue("id")
	tx, e := s.DB.BeginTx(r.Context(), nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var name string
	if e = tx.QueryRow("SELECT name FROM agents WHERE id=?", id).Scan(&name); e != nil {
		return fail(404, "agent not found")
	}
	if b.Name != name {
		return fail(400, "agent name confirmation does not match")
	}
	for _, q := range []string{"DELETE FROM agent_configs WHERE agent_id=?", "DELETE FROM agent_logs WHERE agent_id=?", "DELETE FROM agents WHERE id=?"} {
		if _, e = tx.Exec(q, id); e != nil {
			return e
		}
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	s.audit("agent.delete", id)
	jsonOut(w, 200, map[string]bool{"ok": true})
	return nil
}
func (s *Server) projectHistory(w http.ResponseWriter, r *http.Request) error {
	return s.query(w, r, "SELECT * FROM project_configs WHERE project_id=? ORDER BY version DESC LIMIT 20", r.PathValue("id"))
}
func diskFree(path string) int64 {
	var st syscall.Statfs_t
	if e := syscall.Statfs(path, &st); e != nil {
		return -1
	}
	return int64(st.Bavail) * int64(st.Bsize)
}
