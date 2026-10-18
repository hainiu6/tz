package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/panhui/tz/internal/store"
)

func TestNodeExpiryPersistsAndCanBeCleared(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"expiry-node", "other-node"} {
		if _, err := s.AutoReport(id, "", "192.0.2.1", "v1", store.Metrics{}); err != nil {
			t.Fatal(err)
		}
	}
	app := &server{store: s, adminToken: "admin"}
	update := func(body map[string]any, token string, status int) {
		t.Helper()
		body["name"] = "custom"
		body["note"] = "keep note"
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPut, "/api/nodes/expiry-node", bytes.NewReader(encoded))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		app.api(w, r)
		if w.Code != status {
			t.Fatalf("got %d, want %d: %s", w.Code, status, w.Body.String())
		}
	}
	check := func(expected string) {
		t.Helper()
		_, nodes := app.store.Snapshot()
		for _, n := range nodes {
			if n.ID == "expiry-node" && (n.ExpiresAt != expected || n.Note != "keep note") {
				t.Fatalf("unexpected expiry metadata: %+v", n)
			}
			if n.ID == "other-node" && n.ExpiresAt != "" {
				t.Fatal("another node was edited")
			}
		}
	}
	update(map[string]any{"expiresAt": "2027-03-01T00:15:30+08:00"}, "wrong", http.StatusUnauthorized)
	update(map[string]any{"expiresAt": "2027-03-01T00:15:30+08:00"}, "admin", http.StatusOK)
	const expected = "2027-02-28T16:15:30Z"
	check(expected)
	update(map[string]any{"expiresAt": "2027-02-28T16:15:30.000Z"}, "admin", http.StatusOK)
	check(expected)
	app.store, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	check(expected)
	if _, err := app.store.AutoReport("expiry-node", "", "192.0.2.2", "v2", store.Metrics{}); err != nil {
		t.Fatal(err)
	}
	check(expected)
	for _, value := range []any{"2027-02-30T00:00:00Z", "2027-03-01T00:15:30", "invalid", 123} {
		update(map[string]any{"expiresAt": value}, "admin", http.StatusBadRequest)
		check(expected)
	}
	update(map[string]any{}, "admin", http.StatusOK) // Old clients omit the field.
	check(expected)
	update(map[string]any{"expiresAt": ""}, "admin", http.StatusOK)
	check("")
	app.store, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	check("")
}
