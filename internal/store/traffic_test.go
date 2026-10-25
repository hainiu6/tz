package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestDailyTraffic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 5, 23, 59, 50, 0, trafficLocation)
	s.now = func() time.Time { return now }
	report := func(up, down, uptime uint64) {
		t.Helper()
		_, err := s.AutoReport("daily-node", "", "192.0.2.1", "v1", Metrics{TotalUpload: up, TotalDownload: down, Uptime: uptime})
		if err != nil {
			t.Fatal(err)
		}
	}
	check := func(up, down uint64, date string) {
		t.Helper()
		_, nodes := s.Snapshot()
		n := nodes[0]
		if n.TodayUpload != up || n.TodayDownload != down || n.TrafficDate != date {
			t.Fatalf("got %d/%d on %s, want %d/%d on %s", n.TodayUpload, n.TodayDownload, n.TrafficDate, up, down, date)
		}
	}
	// Existing lifetime counters must not be attributed to today at installation.
	report(10000, 20000, 1000)
	check(0, 0, "2026-09-05")
	now = now.Add(5 * time.Second)
	report(10100, 20400, 1005)
	check(100, 400, "2026-09-05")
	// Panel restart preserves both totals and the counter baseline.
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return now }
	check(100, 400, "2026-09-05")
	now = now.Add(10 * time.Second)
	// Offline nodes show zero after midnight even without a new report.
	check(0, 0, "2026-09-06")
	report(10300, 21000, 1015)
	check(100, 300, "2026-09-06")
	// A reboot does not underflow counters or erase traffic already recorded.
	now = now.Add(3 * time.Second)
	report(20, 40, 2)
	check(100, 300, "2026-09-06")
	now = now.Add(3 * time.Second)
	report(30, 60, 5)
	check(110, 320, "2026-09-06")
	// Counter reset without a reboot (for example, an interface reset).
	now = now.Add(3 * time.Second)
	report(5, 10, 8)
	check(110, 320, "2026-09-06")
	_, nodes := s.Snapshot()
	if nodes[0].YesterdayUpload != 200 || nodes[0].YesterdayDownload != 700 {
		t.Fatalf("incorrect yesterday totals: %+v", nodes[0])
	}
}

func TestDelayedReportsAndTemporaryCounterDropsDoNotInflateTraffic(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, trafficLocation)
	n := Node{}
	const lifetime = uint64(1 << 40)
	n.applyReport("", "", Metrics{TotalUpload: lifetime, TotalDownload: lifetime, Uptime: 10000}, now)
	now = now.Add(30 * time.Second)
	n.applyReport("", "", Metrics{TotalUpload: lifetime + 100, TotalDownload: lifetime + 200, Uptime: 10003}, now)
	if n.TodayUpload != 100 || n.TodayDownload != 200 {
		t.Fatal("arrival delay inflated traffic")
	}
	now = now.Add(3 * time.Second)
	n.applyReport("", "", Metrics{}, now)
	now = now.Add(3 * time.Second)
	n.applyReport("", "", Metrics{TotalUpload: lifetime + 200, TotalDownload: lifetime + 400, Uptime: 10009}, now)
	if n.TodayUpload != 200 || n.TodayDownload != 400 {
		t.Fatal("zero sample recovery inflated traffic")
	}
	now = now.Add(3 * time.Second)
	n.applyReport("", "", Metrics{TotalUpload: lifetime - 500, TotalDownload: lifetime - 1000, Uptime: 10012}, now)
	now = now.Add(3 * time.Second)
	n.applyReport("", "", Metrics{TotalUpload: lifetime + 300, TotalDownload: lifetime + 600, Uptime: 10015}, now)
	if n.TodayUpload != 300 || n.TodayDownload != 600 {
		t.Fatal("counter dip recovery inflated traffic")
	}
}

func TestBootAndInterfaceChangesRebaseline(t *testing.T) {
	now := time.Now()
	n := Node{}
	m := Metrics{BootID: "boot-a", NetworkSet: "eth0", TotalUpload: 1000, TotalDownload: 2000, Uptime: 100}
	n.applyReport("", "", m, now)
	now = now.Add(3 * time.Second)
	m.TotalUpload += 100
	m.TotalDownload += 200
	n.applyReport("", "", m, now)
	now = now.Add(3 * time.Second)
	m.NetworkSet = "eth0,eth1"
	m.TotalUpload += 1 << 40
	m.TotalDownload += 1 << 40
	n.applyReport("", "", m, now)
	now = now.Add(3 * time.Second)
	m.BootID = "boot-b"
	m.TotalUpload = 500
	m.TotalDownload = 900
	n.applyReport("", "", m, now)
	now = now.Add(3 * time.Second)
	m.TotalUpload += 10
	m.TotalDownload += 20
	n.applyReport("", "", m, now)
	if n.TodayUpload != 110 || n.TodayDownload != 220 {
		t.Fatal("boot or interface change counted lifetime counters")
	}
}

func TestYesterdaySurvivesRestartAndOfflineRollover(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, trafficLocation)
	s.now = func() time.Time { return now }
	if _, err = s.AutoReport("yesterday-node", "", "", "", Metrics{TotalUpload: 100, TotalDownload: 200}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if _, err = s.AutoReport("yesterday-node", "", "", "", Metrics{TotalUpload: 300, TotalDownload: 600}); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return now }
	now = now.AddDate(0, 0, 1)
	_, nodes := s.Snapshot()
	if nodes[0].YesterdayUpload != 200 || nodes[0].YesterdayDownload != 400 || nodes[0].TodayUpload != 0 {
		t.Fatal("offline rollover lost yesterday totals")
	}
	now = now.AddDate(0, 0, 1)
	_, nodes = s.Snapshot()
	if nodes[0].YesterdayUpload != 0 || nodes[0].YesterdayDownload != 0 || nodes[0].DayBeforeYesterdayUpload != 200 || nodes[0].DayBeforeYesterdayDownload != 400 {
		t.Fatal("offline rollover lost day-before-yesterday totals")
	}
	now = now.AddDate(0, 0, 1)
	_, nodes = s.Snapshot()
	if nodes[0].DayBeforeYesterdayUpload != 0 || nodes[0].DayBeforeYesterdayDownload != 0 {
		t.Fatal("stale day-before-yesterday totals survived three days")
	}
}

func TestDelayedReportSplitsAcrossThreeTrafficDays(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, trafficLocation)
	n := Node{}
	n.applyReport("", "", Metrics{TotalUpload: 10000, TotalDownload: 20000, Uptime: 1000}, now)
	now = now.Add(48 * time.Hour)
	n.applyReport("", "", Metrics{TotalUpload: 14800, TotalDownload: 29600, Uptime: 173800}, now)
	if n.TodayUpload != 1200 || n.TodayDownload != 2400 {
		t.Fatalf("incorrect today split: %d/%d", n.TodayUpload, n.TodayDownload)
	}
	if n.YesterdayUpload != 2400 || n.YesterdayDownload != 4800 {
		t.Fatalf("incorrect yesterday split: %d/%d", n.YesterdayUpload, n.YesterdayDownload)
	}
	if n.DayBeforeYesterdayUpload != 1200 || n.DayBeforeYesterdayDownload != 2400 {
		t.Fatalf("incorrect day-before-yesterday split: %d/%d", n.DayBeforeYesterdayUpload, n.DayBeforeYesterdayDownload)
	}
}

func TestDailyTrafficLegacyTokenAndMigration(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, trafficLocation)
	s.now = func() time.Time { return now }
	n, err := s.CreateNode("legacy", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate v0.5 data: prior metrics exist, but no daily baseline.
	s.data.Nodes[0].LastSeen = now.Add(-time.Hour)
	s.data.Nodes[0].Metrics = Metrics{TotalUpload: 5000, TotalDownload: 10000}
	if _, err := s.Report(n.AgentToken, "192.0.2.2", "v1", Metrics{TotalUpload: 9000, TotalDownload: 18000}); err != nil {
		t.Fatal(err)
	}
	_, nodes := s.Snapshot()
	if nodes[0].TodayUpload != 0 || nodes[0].TodayDownload != 0 {
		t.Fatal("migration counted historical traffic")
	}
	now = now.Add(3 * time.Second)
	if _, err := s.Report(n.AgentToken, "192.0.2.2", "v1", Metrics{TotalUpload: 9500, TotalDownload: 19000}); err != nil {
		t.Fatal(err)
	}
	_, nodes = s.Snapshot()
	if nodes[0].TodayUpload != 500 || nodes[0].TodayDownload != 1000 {
		t.Fatal("legacy reports did not accumulate traffic")
	}
}
