// Package branding retains installation compatibility across the Sailfin rename.
package branding

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	BrowserRelayPrefix      = "/__sailfin/"
	BrowserBootPath         = BrowserRelayPrefix + "sailfin-boot.js"
	PreviousBrowserBootPath = BrowserRelayPrefix + "boot.js"

	LegacyRelayPrefix = "/__tracelite/"
	LegacyHTML        = `<script type="module" src="/__tracelite/boot.js"></script></head>`
)

// Environment gives explicitly configured Sailfin variables precedence.
func Environment() {
	for _, item := range os.Environ() {
		key, value, _ := strings.Cut(item, "=")
		if !strings.HasPrefix(key, "TRACELITE_") {
			continue
		}
		next := "SAILFIN_" + strings.TrimPrefix(key, "TRACELITE_")
		if _, exists := os.LookupEnv(next); !exists {
			_ = os.Setenv(next, value)
		}
	}
}

// DefaultDBPath preserves an existing database without renaming a live SQLite file.
func DefaultDBPath() string {
	current := filepath.Join("var", "sailfin.db")
	previous := filepath.Join("var", "tracelite.db")
	if _, err := os.Stat(current); os.IsNotExist(err) {
		if _, err := os.Stat(previous); err == nil {
			return previous
		}
	}
	return current
}

// Header also accepts metadata from Nginx configurations applied before the rename.
func Header(r *http.Request, suffix string) string {
	if value := r.Header.Get("X-Sailfin-" + suffix); value != "" {
		return value
	}
	return r.Header.Get("X-TraceLite-" + suffix)
}
