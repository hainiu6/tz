package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestUpgradeAllAndPowerCommands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	for _, id := range []string{"first-node", "second-node"} {
		if _, err := s.AutoReport(id, "", "192.0.2.1", "v1", Metrics{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RequestPower("first-node", "anything"); !errors.Is(err, ErrInvalidPowerAction) {
		t.Fatalf("invalid action: %v", err)
	}
	if err := s.RequestPower("first-node", "reboot"); err != nil {
		t.Fatal(err)
	}
	if err := s.RequestPower("first-node", "shutdown"); !errors.Is(err, ErrPowerPending) {
		t.Fatalf("pending action overwritten: %v", err)
	}
	count, err := s.RequestUpgradeAll()
	if err != nil || count != 2 {
		t.Fatalf("upgrade all count=%d err=%v", count, err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return now }
	old, err := s.AutoReportCommands("first-node", "", "192.0.2.1", "v1", Metrics{}, false)
	if err != nil || !old.Upgrade || old.Power != "" {
		t.Fatalf("old agent must upgrade first: %+v, %v", old, err)
	}
	newAgent, err := s.AutoReportCommands("first-node", "", "192.0.2.1", "v2", Metrics{}, true)
	if err != nil || newAgent.Power != "reboot" || newAgent.Upgrade {
		t.Fatalf("power command not delivered: %+v, %v", newAgent, err)
	}
	next, err := s.AutoReportCommands("first-node", "", "192.0.2.1", "v2", Metrics{}, true)
	if err != nil || !next.Upgrade || next.Power != "" {
		t.Fatalf("upgrade should be retained after power: %+v, %v", next, err)
	}
	again, err := s.AutoReportCommands("first-node", "", "192.0.2.1", "v2", Metrics{}, true)
	if err != nil || again.Power != "" || again.Upgrade {
		t.Fatalf("power delivered twice: %+v, %v", again, err)
	}
	second, err := s.AutoReportCommands("second-node", "", "192.0.2.2", "v1", Metrics{}, true)
	if err != nil || !second.Upgrade {
		t.Fatalf("second node not queued: %+v, %v", second, err)
	}
	now = now.Add(16 * time.Second)
	if err := s.RequestPower("second-node", "shutdown"); !errors.Is(err, ErrNodeOffline) {
		t.Fatalf("offline command accepted: %v", err)
	}
}

func TestPowerCommandExpires(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	if _, err := s.AutoReport("test-node", "", "192.0.2.1", "v2", Metrics{}); err != nil {
		t.Fatal(err)
	}
	if err := s.RequestPower("test-node", "shutdown"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(3 * time.Minute)
	commands, err := s.AutoReportCommands("test-node", "", "192.0.2.1", "v2", Metrics{}, true)
	if err != nil || commands.Power != "" {
		t.Fatalf("expired command executed: %+v, %v", commands, err)
	}
}
