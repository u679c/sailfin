package agent

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sailfin/internal/protocol"
	"strings"
	"time"
)

type Docker struct {
	client  *http.Client
	Allowed map[string]bool
}

func NewDocker(socket string, allowed []string) *Docker {
	m := map[string]bool{}
	for _, id := range allowed {
		m[id] = true
	}
	return &Docker{client: &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}, Allowed: m}
}
func (d *Docker) get(ctx context.Context, path string) (*http.Response, error) {
	req, e := http.NewRequestWithContext(ctx, "GET", "http://docker"+path, nil)
	if e != nil {
		return nil, e
	}
	res, e := d.client.Do(req)
	if e != nil {
		return nil, e
	}
	if res.StatusCode != 200 {
		res.Body.Close()
		return nil, fmt.Errorf("Docker read returned %d", res.StatusCode)
	}
	return res, nil
}
func (d *Docker) Discover(ctx context.Context) ([]protocol.Container, error) {
	res, e := d.get(ctx, "/containers/json?all=1&limit=200")
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	var rows []struct {
		ID    string   `json:"Id"`
		Names []string `json:"Names"`
		Image string   `json:"Image"`
		State string   `json:"State"`
	}
	if e = json.NewDecoder(io.LimitReader(res.Body, 1048576)).Decode(&rows); e != nil {
		return nil, e
	}
	out := []protocol.Container{}
	for _, v := range rows {
		c := protocol.Container{ID: v.ID, Image: v.Image, State: v.State, Collectable: d.Allowed[v.ID]}
		if len(v.Names) > 0 {
			c.Name = strings.TrimPrefix(v.Names[0], "/")
		}
		if c.Collectable {
			detail, e := d.get(ctx, "/containers/"+url.PathEscape(v.ID)+"/json")
			if e != nil {
				return nil, e
			}
			var obj struct {
				HostConfig struct {
					LogConfig struct {
						Type string `json:"Type"`
					} `json:"LogConfig"`
				} `json:"HostConfig"`
			}
			err := json.NewDecoder(io.LimitReader(detail.Body, 1048576)).Decode(&obj)
			detail.Body.Close()
			if err != nil {
				return nil, err
			}
			c.LogDriver = obj.HostConfig.LogConfig.Type
		}
		out = append(out, c)
	}
	return out, nil
}
func (d *Docker) Logs(ctx context.Context, id string, since int64) ([]string, error) {
	if !d.Allowed[id] {
		return nil, fmt.Errorf("container %s is not locally authorized", id)
	}
	res, e := d.get(ctx, fmt.Sprintf("/containers/%s/logs?stdout=1&stderr=1&timestamps=1&tail=all&since=%d", url.PathEscape(id), since))
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	b, e := io.ReadAll(io.LimitReader(res.Body, 262145))
	if e != nil {
		return nil, e
	}
	if len(b) > 262144 {
		return nil, fmt.Errorf("Docker log response exceeds bounded buffer")
	}
	var text strings.Builder
	if len(b) >= 8 && b[0] <= 2 && b[1] == 0 && b[2] == 0 && b[3] == 0 {
		for len(b) >= 8 {
			n := int(binary.BigEndian.Uint32(b[4:8]))
			if n > len(b)-8 {
				return nil, fmt.Errorf("invalid Docker log frame")
			}
			text.Write(b[8 : 8+n])
			b = b[8+n:]
		}
	} else {
		text.Write(b)
	}
	lines := []string{}
	scanner := bufio.NewScanner(strings.NewReader(text.String()))
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}
