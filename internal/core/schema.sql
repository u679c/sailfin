CREATE TABLE IF NOT EXISTS migrations(version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS users(id TEXT PRIMARY KEY, name TEXT UNIQUE NOT NULL, password TEXT NOT NULL, role TEXT NOT NULL DEFAULT 'admin');
CREATE TABLE IF NOT EXISTS auth_sessions(token TEXT PRIMARY KEY,user_id TEXT NOT NULL,csrf TEXT NOT NULL,expires INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS projects(id TEXT PRIMARY KEY,name TEXT NOT NULL,description TEXT NOT NULL,domains TEXT NOT NULL DEFAULT '[]',status TEXT NOT NULL DEFAULT 'active',retention_days INTEGER NOT NULL DEFAULT 30,daily_quota INTEGER NOT NULL DEFAULT 10000,browser_enabled INTEGER NOT NULL DEFAULT 0,collection TEXT NOT NULL DEFAULT '{}',created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS enrollments(token TEXT PRIMARY KEY,project_id TEXT NOT NULL,name TEXT NOT NULL,expires INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS agents(id TEXT PRIMARY KEY,project_id TEXT NOT NULL,name TEXT NOT NULL,token TEXT UNIQUE NOT NULL,enabled INTEGER NOT NULL DEFAULT 1,hostname TEXT NOT NULL DEFAULT '',os TEXT NOT NULL DEFAULT '',arch TEXT NOT NULL DEFAULT '',version TEXT NOT NULL DEFAULT '',last_seen INTEGER NOT NULL DEFAULT 0,metrics TEXT NOT NULL DEFAULT '{}',containers TEXT NOT NULL DEFAULT '[]',error TEXT NOT NULL DEFAULT '',created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS agent_configs(agent_id TEXT NOT NULL,version INTEGER NOT NULL,config TEXT NOT NULL,created_at INTEGER NOT NULL,PRIMARY KEY(agent_id,version));
CREATE TABLE IF NOT EXISTS agent_logs(id INTEGER PRIMARY KEY,agent_id TEXT NOT NULL,message TEXT NOT NULL,created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS events(id TEXT NOT NULL,project_id TEXT NOT NULL,session_id TEXT NOT NULL,visitor_id TEXT NOT NULL,type TEXT NOT NULL,timestamp INTEGER NOT NULL,received_at INTEGER NOT NULL,path TEXT NOT NULL,properties TEXT NOT NULL,source_type TEXT NOT NULL,agent_id TEXT NOT NULL,device TEXT NOT NULL,browser TEXT NOT NULL,os TEXT NOT NULL,device_info TEXT NOT NULL,PRIMARY KEY(project_id,id));
CREATE INDEX IF NOT EXISTS events_project_time ON events(project_id,timestamp);
CREATE INDEX IF NOT EXISTS events_session ON events(project_id,session_id,timestamp);
CREATE INDEX IF NOT EXISTS events_received ON events(project_id,received_at);
CREATE INDEX IF NOT EXISTS events_type_time ON events(type,timestamp);
CREATE TABLE IF NOT EXISTS daily_usage(project_id TEXT NOT NULL,day TEXT NOT NULL,count INTEGER NOT NULL,PRIMARY KEY(project_id,day));
CREATE TABLE IF NOT EXISTS security_counters(name TEXT PRIMARY KEY,count INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS audit_logs(id INTEGER PRIMARY KEY,action TEXT NOT NULL,target TEXT NOT NULL,created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS system_settings(name TEXT PRIMARY KEY,value TEXT NOT NULL);
INSERT OR IGNORE INTO migrations VALUES(1,unixepoch()*1000);

CREATE TABLE IF NOT EXISTS project_configs(project_id TEXT NOT NULL,version INTEGER NOT NULL,config TEXT NOT NULL,created_at INTEGER NOT NULL,PRIMARY KEY(project_id,version));

CREATE TABLE IF NOT EXISTS site_registry (
  project_id TEXT PRIMARY KEY, site_id TEXT NOT NULL DEFAULT '', source TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 0, enabled_since INTEGER NOT NULL DEFAULT 0,
  access_log TEXT NOT NULL DEFAULT '', browser_origin TEXT NOT NULL DEFAULT '',
  require_consent INTEGER NOT NULL DEFAULT 0
);
INSERT OR IGNORE INTO site_registry(project_id,source,enabled)
  SELECT id,'legacy',1 FROM projects WHERE id GLOB 'site_*' AND status='active';
INSERT OR IGNORE INTO migrations VALUES(2,unixepoch()*1000);

CREATE TABLE IF NOT EXISTS web_update_packages (
  job_id TEXT PRIMARY KEY, name TEXT NOT NULL
);
INSERT OR IGNORE INTO migrations VALUES(3,unixepoch()*1000);

CREATE TABLE IF NOT EXISTS click_merge_members (
  project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  page TEXT NOT NULL, element_key TEXT NOT NULL, group_key TEXT NOT NULL,
  PRIMARY KEY(project_id,page,element_key)
);
CREATE INDEX IF NOT EXISTS click_merge_group ON click_merge_members(project_id,page,group_key);
INSERT OR IGNORE INTO migrations VALUES(4,unixepoch()*1000);
