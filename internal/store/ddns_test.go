package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDDNSScheduleAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDDNSCredentials("ak", "sk"); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 31, 12, 0, 0, 0, time.UTC)
	task, err := s.CreateDDNSTask(DDNSTask{Domain: "www.example.com", Type: "A", Target: "1.2.3.4", NextRun: now.Add(-time.Minute), Repeat: "monthly", RunDay: 31})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.DueDDNSTasks(now)) != 1 {
		t.Fatal("expected due task")
	}
	if _, err := s.BeginDDNSRun(task.ID, true, now); err != nil {
		t.Fatal(err)
	}
	got, err := s.DDNSTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	local := got.NextRun.In(time.FixedZone("CST", 8*3600))
	if local.Month() != time.February || local.Day() != 28 {
		t.Fatalf("wrong monthly rollover: %s", local)
	}
	if _, err := s.BeginDDNSRun(task.ID, true, now); err == nil {
		t.Fatal("same occurrence executed twice")
	}
	log := DDNSLog{At: now, Status: "updated", Trigger: "scheduled", From: "1.1.1.1", To: "1.2.3.4"}
	if err := s.CompleteDDNSRun(task.ID, log); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ak, sk := reopened.DDNSCredentials()
	if ak != "ak" || sk != "sk" {
		t.Fatal("credentials not persisted")
	}
	logs, err := reopened.DDNSLogs(task.ID)
	if err != nil || len(logs) != 1 || logs[0].From != "1.1.1.1" {
		t.Fatalf("logs not persisted: %+v, %v", logs, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("data file permissions: %v", info.Mode())
	}
}

func TestOneTimeDDNSCompletion(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	task, err := s.CreateDDNSTask(DDNSTask{Domain: "www.example.com", Type: "A", Target: "1.2.3.4", NextRun: now.Add(-time.Minute), Repeat: "once"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDDNSTaskEnabled(task.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginDDNSRun(task.ID, false, now); err != nil {
		t.Fatal(err)
	}
	paused, _ := s.DDNSTask(task.ID)
	if paused.Completed {
		t.Fatal("manual execution must not finish a paused schedule")
	}
	if err := s.SetDDNSTaskEnabled(task.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginDDNSRun(task.ID, true, now); err != nil {
		t.Fatal(err)
	}
	completed, _ := s.DDNSTask(task.ID)
	if completed.Enabled || !completed.Completed {
		t.Fatalf("one-time task not completed: %+v", completed)
	}
}
