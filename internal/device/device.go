package device

import (
	"github.com/mssola/user_agent"
	"strings"
)

type Info struct {
	Type           string `json:"type"`
	Browser        string `json:"browser"`
	BrowserVersion string `json:"browserVersion"`
	OS             string `json:"os"`
	OSVersion      string `json:"osVersion"`
	Model          string `json:"model"`
	Brand          string `json:"brand"`
}

func Parse(raw string) Info {
	d := Info{Type: "Unknown", Browser: "Unknown", OS: "Unknown", Model: "Unknown", Brand: "Unknown"}
	if raw == "" {
		return d
	}
	u := user_agent.New(raw)
	d.Browser, d.BrowserVersion = u.Browser()
	o := u.OSInfo()
	d.OS = o.Name
	d.OSVersion = o.Version
	switch {
	case u.Bot():
		d.Type = "Bot"
	case u.Platform() == "iPad":
		d.Type = "Tablet"
	case u.Mobile():
		d.Type = "Mobile"
	case u.Platform() != "":
		d.Type = "Desktop"
	}
	if d.Browser == "" {
		d.Browser = "Unknown"
	}
	if d.OS == "" {
		d.OS = "Unknown"
	}
	if m := u.Model(); m != "" {
		d.Model = m
	}
	d.BrowserVersion = strings.Split(d.BrowserVersion, ".")[0]
	d.OSVersion = strings.Split(d.OSVersion, ".")[0]
	return d
}
