package protocol

import (
	"encoding/json"
	"regexp"
)

type Event struct {
	EventID    string         `json:"eventId"`
	ProjectID  string         `json:"projectId"`
	SessionID  string         `json:"sessionId"`
	VisitorID  string         `json:"visitorId"`
	EventType  string         `json:"eventType"`
	Timestamp  int64          `json:"timestamp"`
	Path       string         `json:"path"`
	UA         string         `json:"ua,omitempty"`
	Properties map[string]any `json:"properties"`
}
type Source struct {
	ID     string            `json:"id"`
	Path   string            `json:"path"`
	Parser string            `json:"parser"`
	Fields map[string]string `json:"fields,omitempty"`
}
type Config struct {
	Version         int      `json:"version"`
	Enabled         bool     `json:"enabled"`
	Sources         []Source `json:"sources"`
	DockerTargets   []string `json:"dockerTargets"`
	BatchSize       int      `json:"batchSize"`
	IntervalSeconds int      `json:"intervalSeconds"`
}
type Container struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Image       string `json:"image"`
	State       string `json:"state"`
	LogDriver   string `json:"logDriver"`
	Collectable bool   `json:"collectable"`
}
type Heartbeat struct {
	Hostname   string          `json:"hostname"`
	OS         string          `json:"os"`
	Arch       string          `json:"arch"`
	Version    string          `json:"version"`
	Containers []Container     `json:"containers"`
	Metrics    json.RawMessage `json:"metrics"`
	Error      string          `json:"error"`
}

// SiteBatch is sent by an enrolled host, never by the browser.
type SiteBatch struct {
	SiteID string  `json:"siteId"`
	Name   string  `json:"name"`
	Source string  `json:"source"`
	Events []Event `json:"events"`
}

var siteIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,119}$`)

func ValidSiteID(id string) bool { return siteIDPattern.MatchString(id) }
