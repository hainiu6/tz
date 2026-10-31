package main

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSelfHostedDownloadsAndChecksums(t *testing.T) {
	agent := []byte("embedded current agent")
	panel := []byte("offline panel")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tz-panel-linux-amd64"), panel, 0600); err != nil {
		t.Fatal(err)
	}
	// Old artifacts on disk must not override the agents bundled with this panel.
	if err := os.WriteFile(filepath.Join(dir, "tz-agent-linux-amd64"), []byte("old agent"), 0600); err != nil {
		t.Fatal(err)
	}
	h := downloadHandler(fstest.MapFS{"agents/tz-agent-linux-amd64": {Data: agent}}, dir)
	for name, want := range map[string][]byte{"tz-agent-linux-amd64": agent, "tz-panel-linux-amd64": panel} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/downloads/"+name, nil))
		if w.Code != 200 || w.Body.String() != string(want) || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("unexpected download %s: %d %q", name, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/downloads/checksums.txt", nil))
	for name, data := range map[string][]byte{"tz-agent-linux-amd64": agent, "tz-panel-linux-amd64": panel} {
		if !strings.Contains(w.Body.String(), fmt.Sprintf("%x  %s\n", sha256.Sum256(data), name)) {
			t.Fatalf("manifest does not match served bytes: %q", w.Body.String())
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/downloads/tz-agent-linux-amd64", nil)
	req.Header.Set("Range", "bytes=0-7")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusPartialContent || w.Body.String() != string(agent[:8]) {
		t.Fatal("range download failed")
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodHead, "/downloads/tz-agent-linux-amd64", nil))
	if w.Code != 200 || w.Body.Len() != 0 {
		t.Fatal("HEAD download failed")
	}
}

func TestDownloadAllowlistAndUnavailablePackages(t *testing.T) {
	h := downloadHandler(fstest.MapFS{}, t.TempDir())
	for _, name := range []string{"", "data.json", "../data.json", "agents/README.txt", "tz-agent-linux-amd64.exe"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/downloads/"+name, nil))
		if w.Code != http.StatusNotFound {
			t.Fatalf("unexpected exposure: %s => %d", name, w.Code)
		}
	}
	for _, name := range []string{"tz-agent-linux-amd64", "tz-panel-linux-arm64", "checksums.txt"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/downloads/"+name, nil))
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("missing package returned %d", w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/downloads/checksums.txt", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatal("downloads must be read-only")
	}
}
