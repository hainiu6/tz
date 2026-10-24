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

func TestTrafficQuotaPersistenceValidationAndClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"quota-node", "other-node"} {
		if _, err := s.AutoReport(id, "", "192.0.2.1", "v1", store.Metrics{}); err != nil {
			t.Fatal(err)
		}
	}
	app := &server{store: s, adminToken: "admin"}
	update := func(body map[string]any, token string, status int) {
		t.Helper()
		body["name"], body["note"] = "custom", "keep note"
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPut, "/api/nodes/quota-node", bytes.NewReader(encoded))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		app.api(w, r)
		if w.Code != status {
			t.Fatalf("got %d, want %d: %s", w.Code, status, w.Body.String())
		}
	}
	const expiry = "2027-03-01T00:00:00Z"
	check := func(value, unit string) {
		t.Helper()
		_, nodes := app.store.Snapshot()
		if len(nodes) != 2 {
			t.Fatal("node count changed")
		}
		for _, node := range nodes {
			if node.ID == "quota-node" && (node.TrafficQuota != value || node.TrafficQuotaUnit != unit || node.ExpiresAt != expiry || node.Note != "keep note") {
				t.Fatalf("incorrect metadata: %+v", node)
			}
			if node.ID == "other-node" && (node.TrafficQuota != "" || node.TrafficQuotaUnit != "") {
				t.Fatal("another node was changed")
			}
		}
	}
	update(map[string]any{"trafficQuota": "500", "trafficQuotaUnit": "GB"}, "wrong", http.StatusUnauthorized)
	update(map[string]any{"trafficQuota": "500", "trafficQuotaUnit": "GB", "expiresAt": expiry}, "admin", http.StatusOK)
	check("500", "GB")
	app.store, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	check("500", "GB")
	update(map[string]any{"trafficQuota": "1.5", "trafficQuotaUnit": "tb"}, "admin", http.StatusOK)
	check("1.5", "TB")
	update(map[string]any{}, "admin", http.StatusOK) // Older editors omit these fields.
	check("1.5", "TB")
	if _, err := app.store.AutoReport("quota-node", "", "192.0.2.2", "v2", store.Metrics{}); err != nil {
		t.Fatal(err)
	}
	if err := app.store.ResetNodeTraffic("quota-node"); err != nil {
		t.Fatal(err)
	}
	check("1.5", "TB")
	for _, unit := range []string{"B", "KB", "MB", "GB", "TB", "PB"} {
		update(map[string]any{"trafficQuota": "2.5", "trafficQuotaUnit": unit}, "admin", http.StatusOK)
		check("2.5", unit)
	}
	update(map[string]any{"trafficQuota": "500"}, "admin", http.StatusOK)
	check("500", "GB")
	for _, body := range []map[string]any{
		{"trafficQuota": "-1", "expiresAt": "2028-01-01T00:00:00Z"},
		{"trafficQuota": "NaN"}, {"trafficQuota": "+Inf"}, {"trafficQuota": "1e1000"}, {"trafficQuota": "invalid"},
		{"trafficQuota": "10", "trafficQuotaUnit": "XB"}, {"trafficQuotaUnit": "TB"}, {"trafficQuota": 500},
	} {
		update(body, "admin", http.StatusBadRequest)
		check("500", "GB")
	}
	update(map[string]any{"trafficQuota": "0", "trafficQuotaUnit": "MB"}, "admin", http.StatusOK)
	check("0", "MB")
	update(map[string]any{"trafficQuota": "", "trafficQuotaUnit": ""}, "admin", http.StatusOK)
	check("", "")
	app.store, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	check("", "")
}
