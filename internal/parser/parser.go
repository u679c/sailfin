package parser

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sailfin/internal/protocol"
	"strconv"
	"strings"
	"time"
)

type Record struct {
	Time             int64
	Path, UA, Method string
	Status, Bytes    int
}
type Adapter interface {
	Parse(string, map[string]string) (Record, error)
}
type AdapterFunc func(string, map[string]string) (Record, error)

func (f AdapterFunc) Parse(s string, m map[string]string) (Record, error) { return f(s, m) }

var combined = regexp.MustCompile(`^\S+ \S+ \S+ \[([^\]]+)\] "(\S+) ([^ ]+) [^"]+" (\d{3}) (\d+|-) "[^"]*" "([^"]*)"`)
var adapters = map[string]Adapter{"nginx": AdapterFunc(parseCombined), "apache": AdapterFunc(parseCombined), "json": AdapterFunc(parseJSON), "caddy": AdapterFunc(parseCaddy), "docker-json": AdapterFunc(parseDocker)}

func Parse(kind, line string, fields map[string]string) (Record, error) {
	a, ok := adapters[kind]
	if !ok {
		return Record{}, errors.New("unsupported parser")
	}
	r, e := a.Parse(line, fields)
	if e != nil {
		return r, e
	}
	u, e := url.Parse(r.Path)
	if e != nil || !strings.HasPrefix(u.Path, "/") {
		return r, errors.New("invalid request path")
	}
	r.Path = u.EscapedPath()
	if r.Time == 0 {
		r.Time = time.Now().UnixMilli()
	}
	return r, nil
}
func parseCombined(line string, _ map[string]string) (Record, error) {
	m := combined.FindStringSubmatch(line)
	if m == nil {
		return Record{}, errors.New("not a combined access log")
	}
	t, e := time.Parse("02/Jan/2006:15:04:05 -0700", m[1])
	if e != nil {
		return Record{}, e
	}
	status, _ := strconv.Atoi(m[4])
	bytes, _ := strconv.Atoi(m[5])
	return Record{t.UnixMilli(), m[3], m[6], m[2], status, bytes}, nil
}
func parseJSON(line string, fields map[string]string) (Record, error) {
	var m map[string]any
	if e := json.Unmarshal([]byte(line), &m); e != nil {
		return Record{}, e
	}
	get := func(k string) any {
		if f := fields[k]; f != "" {
			return m[f]
		}
		return m[k]
	}
	path, _ := get("path").(string)
	ua, _ := get("ua").(string)
	method, _ := get("method").(string)
	status, _ := get("status").(float64)
	bytes, _ := get("bytes").(float64)
	ts := int64(0)
	switch t := get("timestamp").(type) {
	case float64:
		ts = int64(t)
		if ts < 100000000000 {
			ts *= 1000
		}
	case string:
		v, e := time.Parse(time.RFC3339Nano, t)
		if e != nil {
			return Record{}, e
		}
		ts = v.UnixMilli()
	}
	return Record{ts, path, ua, method, int(status), int(bytes)}, nil
}
func parseCaddy(line string, _ map[string]string) (Record, error) {
	var v struct {
		TS      float64 `json:"ts"`
		Status  int     `json:"status"`
		Size    int     `json:"size"`
		Request struct {
			URI     string              `json:"uri"`
			Method  string              `json:"method"`
			Headers map[string][]string `json:"headers"`
		} `json:"request"`
	}
	if e := json.Unmarshal([]byte(line), &v); e != nil {
		return Record{}, e
	}
	ua := ""
	if a := v.Request.Headers["User-Agent"]; len(a) > 0 {
		ua = a[0]
	}
	return Record{int64(v.TS * 1000), v.Request.URI, ua, v.Request.Method, v.Status, v.Size}, nil
}
func parseDocker(line string, fields map[string]string) (Record, error) {
	var v struct {
		Log string `json:"log"`
	}
	if e := json.Unmarshal([]byte(line), &v); e != nil {
		return Record{}, e
	}
	if strings.HasPrefix(strings.TrimSpace(v.Log), "{") {
		return parseJSON(v.Log, fields)
	}
	return parseCombined(v.Log, fields)
}
func Event(r Record, p, id string) protocol.Event {
	return protocol.Event{EventID: id, ProjectID: p, EventType: "http_request", Timestamp: r.Time, Path: r.Path, UA: r.UA, Properties: map[string]any{"method": r.Method, "status": float64(r.Status), "bytes": float64(r.Bytes)}}
}
func Validate(r Record) error {
	if r.Status < 100 || r.Status > 599 {
		return fmt.Errorf("invalid HTTP status %d", r.Status)
	}
	return nil
}
