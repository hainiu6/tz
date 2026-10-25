package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/huaweicloud/huaweicloud-sdk-go-v3/services/dns/v2/model"
	"github.com/hainiu3/tz/internal/store"
)

func TestNormalizeDDNSTask(t *testing.T) {
	for _, tc := range []struct{ name, kind, target, want string }{
		{"WWW.Example.COM.", "", "1.2.3.4", ""},
		{"www.example.com", "A", "1.2.3.999", "A 记录目标必须是 IPv4 地址"},
		{"www.example.com", "CNAME", "target.example.com.", ""},
		{"invalid name", "A", "1.2.3.4", "解析域名格式无效"},
	} {
		task := store.DDNSTask{Domain: tc.name, Type: tc.kind, Target: tc.target, NextRun: time.Now().Add(time.Hour)}
		if got := normalizeDDNSTask(&task); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestDDNSAPIKeepsSecretsPrivate(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatal(err)
	}
	app := &server{store: st, adminToken: "test-token"}
	request := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/"+path, bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer test-token")
		out := httptest.NewRecorder()
		app.api(out, req)
		return out
	}
	if out := request("PUT", "ddns/credentials", `{"accessKeyId":"AK_PRIVATE","secretAccessKey":"SK_PRIVATE"}`); out.Code != 200 {
		t.Fatal(out.Body)
	}
	for _, path := range []string{"ddns", "dashboard"} {
		out := request("GET", path, "")
		if out.Code != 200 || strings.Contains(out.Body.String(), "AK_PRIVATE") || strings.Contains(out.Body.String(), "SK_PRIVATE") {
			t.Fatalf("secrets exposed at %s: %s", path, out.Body)
		}
	}
	newTask := map[string]any{"domain": "www.example.com", "type": "A", "target": "1.2.3.4", "nextRun": time.Now().Add(time.Hour).Format(time.RFC3339), "repeat": "daily"}
	encoded, _ := json.Marshal(newTask)
	created := request("POST", "ddns/tasks", string(encoded))
	if created.Code != 200 {
		t.Fatal(created.Body)
	}
	var task store.DDNSTask
	if err := json.Unmarshal(created.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	if task.ID == "" || !task.Enabled {
		t.Fatalf("invalid task: %+v", task)
	}
	if out := request("PUT", "ddns/tasks/"+task.ID+"/enabled", `{"enabled":false}`); out.Code != 200 {
		t.Fatal(out.Body)
	}
	if got, _ := st.DDNSTask(task.ID); got.Enabled {
		t.Fatal("task not paused")
	}
	if out := request("DELETE", "ddns/tasks/"+task.ID, ""); out.Code != 200 {
		t.Fatal(out.Body)
	}
	if out := request("GET", "ddns/tasks/"+task.ID+"/logs", ""); out.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", out.Code)
	}
}

type fakeHuaweiDNS struct {
	zones            []model.PublicZoneResp
	records          []model.ListRecordSets
	created, updated bool
}

func (f *fakeHuaweiDNS) ListPublicZones(*model.ListPublicZonesRequest) (*model.ListPublicZonesResponse, error) {
	return &model.ListPublicZonesResponse{Zones: &f.zones}, nil
}
func (f *fakeHuaweiDNS) ListRecordSetsByZone(*model.ListRecordSetsByZoneRequest) (*model.ListRecordSetsByZoneResponse, error) {
	return &model.ListRecordSetsByZoneResponse{Recordsets: &f.records}, nil
}
func (f *fakeHuaweiDNS) CreateRecordSet(*model.CreateRecordSetRequest) (*model.CreateRecordSetResponse, error) {
	f.created = true
	return &model.CreateRecordSetResponse{}, nil
}
func (f *fakeHuaweiDNS) UpdateRecordSet(*model.UpdateRecordSetRequest) (*model.UpdateRecordSetResponse, error) {
	f.updated = true
	return &model.UpdateRecordSetResponse{}, nil
}
func strptr(s string) *string { return &s }

func TestUpdateHuaweiRecord(t *testing.T) {
	fake := &fakeHuaweiDNS{zones: []model.PublicZoneResp{{Id: strptr("zone"), Name: strptr("example.com.")}}}
	task := store.DDNSTask{Domain: "www.example.com", Type: "A", Target: "1.2.3.4"}
	if old, status, err := updateHuaweiRecord(fake, task); err != nil || old != "" || status != "created" || !fake.created {
		t.Fatalf("create: old=%q status=%q err=%v", old, status, err)
	}
	values := []string{"1.2.3.4"}
	fake.records = []model.ListRecordSets{{Id: strptr("record"), Name: strptr("www.example.com."), Type: strptr("A"), Records: &values}}
	fake.created = false
	if old, status, err := updateHuaweiRecord(fake, task); err != nil || old != "1.2.3.4" || status != "unchanged" || fake.updated {
		t.Fatalf("unchanged: old=%q status=%q err=%v", old, status, err)
	}
	task.Target = "5.6.7.8"
	if old, status, err := updateHuaweiRecord(fake, task); err != nil || old != "1.2.3.4" || status != "updated" || !fake.updated {
		t.Fatalf("update: old=%q status=%q err=%v", old, status, err)
	}
	fake.records = append(fake.records, fake.records[0])
	if _, _, err := updateHuaweiRecord(fake, task); err == nil {
		t.Fatal("expected ambiguous record error")
	}
}
