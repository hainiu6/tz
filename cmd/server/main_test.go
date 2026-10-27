package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPersistAdminTokenEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tz-panel.env")
	if err := os.WriteFile(path, []byte("TZ_ADMIN_TOKEN=old-token\nTZ_LISTEN=:8080\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := persistAdminTokenEnv(path, "new-token-123"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(b)
	if !strings.Contains(content, "TZ_ADMIN_TOKEN=\"new-token-123\"\n") || !strings.Contains(content, "TZ_LISTEN=:8080\n") {
		t.Fatalf("unexpected environment file: %q", content)
	}
}
