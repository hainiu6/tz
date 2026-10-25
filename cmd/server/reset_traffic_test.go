package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/hainiu3/tz/internal/store"
)

func TestResetTrafficRequiresAdminAndTargetsOneNode(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"target", "other"} {
		if _, err := s.AutoReport(id, "", "192.0.2.1", "v1", store.Metrics{TotalUpload: 1000, TotalDownload: 2000}); err != nil {
			t.Fatal(err)
		}
	}
	app := &server{store: s, adminToken: "admin", agentToken: "agent"}
	for _, tc := range []struct {
		id, token string
		status    int
	}{
		{"target", "", http.StatusUnauthorized},
		{"target", "agent", http.StatusUnauthorized},
		{"missing", "admin", http.StatusNotFound},
		{"target", "admin", http.StatusOK},
	} {
		r := httptest.NewRequest(http.MethodPost, "/api/nodes/"+tc.id+"/reset-traffic", nil)
		r.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		app.api(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s/%s: got %d, want %d", tc.id, tc.token, w.Code, tc.status)
		}
	}
	_, nodes := s.Snapshot()
	for _, n := range nodes {
		if n.ID == "target" && (n.TotalUpload != 0 || n.TotalDownload != 0) {
			t.Fatal("target was not cleared")
		}
		if n.ID == "other" && (n.TotalUpload != 1000 || n.TotalDownload != 2000) {
			t.Fatal("other node changed")
		}
	}
}
