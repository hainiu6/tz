package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var agentDownloads = []string{"tz-agent-linux-amd64", "tz-agent-linux-arm64", "tz-agent-linux-armv7"}
var panelDownloads = []string{"tz-panel-linux-amd64", "tz-panel-linux-arm64"}

// Only named release artifacts are public; never expose the data directory or a listing.
func downloadHandler(agents fs.FS, directory string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/downloads/")
		w.Header().Set("Cache-Control", "no-store")
		for _, artifact := range agentDownloads {
			if name != artifact {
				continue
			}
			data, err := fs.ReadFile(agents, "agents/"+artifact)
			if err != nil {
				http.Error(w, "agent unavailable: deploy a panel built with scripts/build-release.sh", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", artifact))
			http.ServeContent(w, r, artifact, time.Time{}, bytes.NewReader(data))
			return
		}
		for _, artifact := range panelDownloads {
			if name != artifact {
				continue
			}
			file, err := os.Open(filepath.Join(directory, artifact))
			if err != nil {
				http.Error(w, "panel package unavailable: install the complete offline bundle", http.StatusServiceUnavailable)
				return
			}
			defer file.Close()
			info, err := file.Stat()
			if err != nil || !info.Mode().IsRegular() {
				http.Error(w, "package unavailable", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			http.ServeContent(w, r, artifact, info.ModTime(), file)
			return
		}
		if name != "checksums.txt" {
			http.NotFound(w, r)
			return
		}
		var manifest strings.Builder
		for _, artifact := range agentDownloads {
			if data, err := fs.ReadFile(agents, "agents/"+artifact); err == nil {
				fmt.Fprintf(&manifest, "%x  %s\n", sha256.Sum256(data), artifact)
			}
		}
		for _, artifact := range panelDownloads {
			file, err := os.Open(filepath.Join(directory, artifact))
			if err != nil {
				continue
			}
			info, statErr := file.Stat()
			hash := sha256.New()
			_, readErr := io.Copy(hash, io.LimitReader(file, 128<<20))
			file.Close()
			if statErr == nil && info.Mode().IsRegular() && info.Size() <= 128<<20 && readErr == nil {
				fmt.Fprintf(&manifest, "%x  %s\n", hash.Sum(nil), artifact)
			}
		}
		if manifest.Len() == 0 {
			http.Error(w, "packages unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		http.ServeContent(w, r, "checksums.txt", time.Time{}, strings.NewReader(manifest.String()))
	})
}
