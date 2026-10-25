package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/hainiu3/tz/internal/store"
)

func TestSpeedTestAPIEnrollmentAndLegacyAgents(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "enrollment", true: "legacy"}[legacy], func(t *testing.T) {
			s, err := store.Open(filepath.Join(t.TempDir(), "data.json"))
			if err != nil {
				t.Fatal(err)
			}
			app := &server{store: s, adminToken: "admin", agentToken: "shared"}
			id, agentToken := "speed-node-one", "shared"
			if legacy {
				node, err := s.CreateNode("legacy", "", 0)
				if err != nil {
					t.Fatal(err)
				}
				id, agentToken = node.ID, node.AgentToken
			}
			call := func(method, path, token string, body any, agent bool, status int) map[string]json.RawMessage {
				t.Helper()
				encoded, _ := json.Marshal(body)
				r := httptest.NewRequest(method, "/api/"+path, bytes.NewReader(encoded))
				if agent {
					r.Header.Set("X-Agent-Token", token)
				} else {
					r.Header.Set("Authorization", "Bearer "+token)
				}
				w := httptest.NewRecorder()
				app.api(w, r)
				if w.Code != status {
					t.Fatalf("%s %s got %d want %d: %s", method, path, w.Code, status, w.Body.String())
				}
				var out map[string]json.RawMessage
				if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
					t.Fatal(err)
				}
				return out
			}
			report := func(result *store.SpeedTestResult) map[string]json.RawMessage {
				return call("POST", "report", agentToken, map[string]any{"nodeId": id, "version": "test", "speedTestSupported": true, "speedTestResult": result}, true, 200)
			}
			report(nil)
			path := "nodes/" + id + "/speedtest"
			call("POST", path, "wrong", nil, false, 401)
			call("GET", path, "wrong", nil, false, 401)
			out := call("POST", path, "admin", nil, false, 200)
			var job store.SpeedTest
			if err := json.Unmarshal(out["speedTest"], &job); err != nil {
				t.Fatal(err)
			}
			if job.ID == "" || job.Status != "pending" {
				t.Fatalf("invalid job: %+v", job)
			}
			call("POST", path, "admin", nil, false, 409)
			var delivered string
			out = report(nil)
			json.Unmarshal(out["speedTestId"], &delivered)
			if delivered != job.ID {
				t.Fatalf("command=%q want %q", delivered, job.ID)
			}
			out = report(nil)
			json.Unmarshal(out["speedTestId"], &delivered)
			if delivered != "" {
				t.Fatal("duplicate command")
			}
			report(&store.SpeedTestResult{ID: job.ID, UploadBPS: 1250000, DownloadBPS: 2500000})
			out = call("GET", path, "admin", nil, false, 200)
			json.Unmarshal(out["speedTest"], &job)
			if job.Status != "completed" || job.UploadBPS != 1250000 || job.DownloadBPS != 2500000 {
				t.Fatalf("invalid result: %+v", job)
			}
			call("DELETE", path, "admin", nil, false, 405)
			call("POST", "nodes/not-found/speedtest", "admin", nil, false, 404)
			call("POST", "report", "wrong", map[string]any{"nodeId": id, "speedTestSupported": true}, true, 401)
		})
	}
}

func TestSpeedTestAPIRequiresOnlineCapableNode(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.CreateNode("offline", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	app := &server{store: s, adminToken: "admin"}
	check := func() {
		r := httptest.NewRequest("POST", "/api/nodes/"+node.ID+"/speedtest", nil)
		r.Header.Set("Authorization", "Bearer admin")
		w := httptest.NewRecorder()
		app.api(w, r)
		if w.Code != 409 {
			t.Fatalf("unsupported test accepted: %d", w.Code)
		}
	}
	check()
	if _, err := s.Report(node.AgentToken, "192.0.2.1", "old", store.Metrics{}); err != nil {
		t.Fatal(err)
	}
	check()
}
