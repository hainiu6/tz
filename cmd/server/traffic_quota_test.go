package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/hainiu6/tz/internal/store"
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
	check("500", "TB")
	update(map[string]any{"trafficQuota": "1.5", "trafficQuotaUnit": ""}, "admin", http.StatusOK)
	check("1.5", "TB")
	for _, body := range []map[string]any{
		{"trafficQuota": "-1", "expiresAt": "2028-01-01T00:00:00Z"},
		{"trafficQuota": "NaN"}, {"trafficQuota": "+Inf"}, {"trafficQuota": "1e1000"}, {"trafficQuota": "invalid"},
		{"trafficQuota": "10", "trafficQuotaUnit": "XB"}, {"trafficQuotaUnit": "TB"}, {"trafficQuota": 500},
	} {
		update(body, "admin", http.StatusBadRequest)
		check("1.5", "TB")
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

func TestTrafficExhaustedPersistenceAndClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"marked", "other"} {
		if _, err := s.AutoReport(id, "", "192.0.2.1", "v1", store.Metrics{TotalUpload: 1000, TotalDownload: 2000}); err != nil {
			t.Fatal(err)
		}
	}
	app := &server{store: s, adminToken: "admin"}
	update := func(body map[string]any, token string, status int) {
		t.Helper()
		body["name"] = "test"
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPut, "/api/nodes/marked", bytes.NewReader(encoded))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		app.api(w, r)
		if w.Code != status {
			t.Fatalf("got %d, want %d: %s", w.Code, status, w.Body.String())
		}
	}
	check := func(want bool) store.Node {
		t.Helper()
		_, nodes := app.store.Snapshot()
		var marked store.Node
		for _, node := range nodes {
			if node.ID == "marked" {
				marked = node
				if node.TrafficExhausted != want {
					t.Fatalf("trafficExhausted = %v, want %v", node.TrafficExhausted, want)
				}
			} else if node.TrafficExhausted {
				t.Fatal("another node was marked")
			}
		}
		if marked.ID == "" {
			t.Fatal("marked node missing")
		}
		return marked
	}
	before := check(false)
	update(map[string]any{"trafficExhausted": true}, "wrong", http.StatusUnauthorized)
	check(false)
	update(map[string]any{"trafficExhausted": true}, "admin", http.StatusOK)
	after := check(true)
	if before.Metrics != after.Metrics || before.TodayUpload != after.TodayUpload || before.TodayDownload != after.TodayDownload || before.TrafficQuota != after.TrafficQuota {
		t.Fatal("manual marking changed traffic statistics or quota")
	}
	app.store, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	check(true)
	update(map[string]any{}, "admin", http.StatusOK) // Older clients preserve the marker.
	check(true)
	update(map[string]any{"trafficExhausted": "true"}, "admin", http.StatusBadRequest)
	check(true)
	update(map[string]any{"trafficExhausted": false, "trafficQuota": "invalid"}, "admin", http.StatusBadRequest)
	check(true)
	if _, err := app.store.AutoReport("marked", "", "192.0.2.2", "v2", store.Metrics{TotalUpload: 1500, TotalDownload: 3000}); err != nil {
		t.Fatal(err)
	}
	check(true)
	if err := app.store.ResetNodeTraffic("marked"); err != nil {
		t.Fatal(err)
	}
	check(true)
	update(map[string]any{"trafficQuota": "", "trafficQuotaUnit": ""}, "admin", http.StatusOK)
	check(true)
	update(map[string]any{"trafficExhausted": false}, "admin", http.StatusOK)
	check(false)
	app.store, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	check(false)
}
