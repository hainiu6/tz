package store

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestResetTrafficPersistsAndDoesNotRestoreHistoricalCounters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, trafficLocation)
	s.now = func() time.Time { return now }
	report := func(id string, up, down, uptime uint64, boot string) {
		t.Helper()
		_, err := s.AutoReport(id, "", "192.0.2.1", "v1", Metrics{TotalUpload: up, TotalDownload: down, Uptime: uptime, BootID: boot, NetworkSet: "eth0"})
		if err != nil {
			t.Fatal(err)
		}
	}
	report("reset-node", 10000, 20000, 100, "boot-1")
	report("other-node", 30000, 40000, 100, "boot-1")
	for i := 1; i <= 3; i++ {
		now = now.Add(24 * time.Hour)
		report("reset-node", 10000+uint64(i)*1000, 20000+uint64(i)*2000, 100+uint64(i)*86400, "boot-1")
	}
	if err := s.UpdateNode("reset-node", "custom", "keep note", nil, 9); err != nil {
		t.Fatal(err)
	}
	lastSeen := s.data.Nodes[0].LastSeen
	now = now.Add(3 * 24 * time.Hour) // Reset also works while the node is offline.
	if err := s.ResetNodeTraffic("reset-node"); err != nil {
		t.Fatal(err)
	}
	check := func(up, down uint64) {
		t.Helper()
		_, nodes := s.Snapshot()
		for _, n := range nodes {
			if n.ID == "other-node" {
				if n.TotalUpload != 30000 || n.TotalDownload != 40000 {
					t.Fatal("another node was reset")
				}
				continue
			}
			if n.TotalUpload != up || n.TotalDownload != down || n.TodayUpload != up || n.TodayDownload != down || n.YesterdayUpload != 0 || n.YesterdayDownload != 0 || n.DayBeforeYesterdayUpload != 0 || n.DayBeforeYesterdayDownload != 0 {
				t.Fatalf("incorrect reset statistics: %+v", n)
			}
			if n.Name != "custom" || n.Note != "keep note" || n.Sort != 9 {
				t.Fatal("reset changed node metadata")
			}
		}
	}
	check(0, 0)
	if !s.data.Nodes[0].LastSeen.Equal(lastSeen) {
		t.Fatal("reset changed online status")
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return now }
	now = now.Add(time.Second)
	report("reset-node", 900000, 1800000, 900000, "boot-1") // First sample establishes the new baseline.
	check(0, 0)
	now = now.Add(time.Second)
	report("reset-node", 900100, 1800200, 900001, "boot-1")
	check(100, 200)
	now = now.Add(time.Second)
	report("reset-node", 800000, 1600000, 900002, "boot-1")
	now = now.Add(time.Second)
	report("reset-node", 900100, 1800200, 900003, "boot-1")
	check(100, 200) // A temporary counter drop must not restore old traffic.
	now = now.Add(time.Second)
	report("reset-node", 10, 20, 1, "boot-2")
	now = now.Add(time.Second)
	report("reset-node", 60, 120, 2, "boot-2")
	check(150, 300) // Lifetime since reset survives an agent reboot.
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return now }
	check(150, 300)
	now = now.Add(24 * time.Hour)
	report("reset-node", 160, 320, 86402, "boot-2")
	_, nodes := s.Snapshot()
	for _, n := range nodes {
		if n.ID == "reset-node" && (n.TotalUpload != 250 || n.TotalDownload != 500 || n.YesterdayUpload == 0 || n.TodayUpload == 0) {
			t.Fatalf("day rollover lost cumulative traffic: %+v", n)
		}
	}
	if err := s.ResetNodeTraffic("reset-node"); err != nil {
		t.Fatal(err)
	}
	check(0, 0)
	if err := s.ResetNodeTraffic("missing"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing node: %v", err)
	}
}

func TestResetTrafficRollsBackOnSaveFailure(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AutoReport("node", "", "192.0.2.1", "v1", Metrics{TotalUpload: 100}); err != nil {
		t.Fatal(err)
	}
	previous := s.data.Nodes[0]
	s.path = filepath.Join(t.TempDir(), "blocked")
	if err := os.Mkdir(s.path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetNodeTraffic("node"); err == nil {
		t.Fatal("expected persistence failure")
	}
	if !reflect.DeepEqual(previous, s.data.Nodes[0]) {
		t.Fatal("failed reset mutated statistics")
	}
}
