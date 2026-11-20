package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"sailfin/internal/protocol"
)

type Site struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	AccessLog      string `json:"accessLog,omitempty"`
	BrowserOrigin  string `json:"browserOrigin,omitempty"`
	RequireConsent bool   `json:"requireConsent,omitempty"`
}

func LoadSites(path string) ([]Site, error) {
	if path == "" {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	const maxSize = 64 << 10
	raw, err := io.ReadAll(io.LimitReader(file, maxSize+1))
	if err != nil {
		return nil, err
	}
	return DecodeSites(raw)
}

func DecodeSites(raw []byte) ([]Site, error) {
	if len(raw) > 64<<10 {
		return nil, errors.New("site config exceeds 64 KiB")
	}
	var sites []Site
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&sites); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("site config must contain a single JSON array")
	}
	return sites, nil
}

// ValidateSites checks the settings before a deployment writes them to disk.
func ValidateSites(sites []Site) error {
	_, err := indexSites(sites)
	return err
}

func indexSites(sites []Site) (map[string]Site, error) {
	if len(sites) > 50 {
		return nil, errors.New("at most 50 sites may be configured per host")
	}
	index := make(map[string]Site, len(sites))
	logs := make(map[string]bool, len(sites))
	for _, site := range sites {
		site.Name = strings.TrimSpace(site.Name)
		if !protocol.ValidSiteID(site.ID) || site.Name == "" || len(site.Name) > 120 {
			return nil, errors.New("every site requires a valid ID and name")
		}
		if _, ok := index[site.ID]; ok {
			return nil, fmt.Errorf("duplicate site ID %s", site.ID)
		}
		if site.AccessLog == "" && site.BrowserOrigin == "" {
			return nil, fmt.Errorf("site %s requires accessLog or browserOrigin", site.ID)
		}
		if site.AccessLog != "" {
			if !filepath.IsAbs(site.AccessLog) {
				return nil, fmt.Errorf("site %s accessLog must be absolute", site.ID)
			}
			site.AccessLog = filepath.Clean(site.AccessLog)
			if logs[site.AccessLog] {
				return nil, fmt.Errorf("accessLog must be dedicated to site %s", site.ID)
			}
			logs[site.AccessLog] = true
		}
		if site.BrowserOrigin != "" {
			u, err := url.Parse(site.BrowserOrigin)
			if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") ||
				u.User != nil || site.BrowserOrigin != u.Scheme+"://"+u.Host {
				return nil, fmt.Errorf("site %s browserOrigin must be an exact HTTP(S) origin", site.ID)
			}
		}
		index[site.ID] = site
	}
	return index, nil
}
