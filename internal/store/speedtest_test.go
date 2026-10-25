package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSpeedTestCommandsResultsPersistenceAndExpiry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	report := func(id string, info SpeedTestReport) NodeCommands {
		t.Helper()
		commands, err := s.AutoReportCommands(id, "", "192.0.2.1", "vnew", Metrics{}, true, info)
		if err != nil {
			t.Fatal(err)
		}
		return commands
	}
	report("node-one", SpeedTestReport{})
	if _, err := s.RequestSpeedTest("node-one"); !errors.Is(err, ErrSpeedTestUnsupported) {
		t.Fatal(err)
	}
	report("node-one", SpeedTestReport{Supported: true})
	report("node-two", SpeedTestReport{Supported: true})
	job, err := s.RequestSpeedTest("node-one")
	if err != nil || job.Status != "pending" {
		t.Fatalf("job=%+v err=%v", job, err)
	}
	if _, err := s.RequestSpeedTest("node-one"); !errors.Is(err, ErrSpeedTestBusy) {
		t.Fatal(err)
	}
	if err := s.RequestUpgrade("node-one"); err != nil {
		t.Fatal(err)
	}
	if command := report("node-one", SpeedTestReport{Supported: true}); !command.Upgrade || command.SpeedTestID != "" {
		t.Fatalf("upgrade priority: %+v", command)
	}
	command := report("node-one", SpeedTestReport{Supported: true})
	if command.SpeedTestID != job.ID {
		t.Fatalf("missing job: %+v", command)
	}
	if command := report("node-one", SpeedTestReport{Supported: true}); command.SpeedTestID != "" {
		t.Fatal("job delivered twice")
	}
	result := &SpeedTestResult{ID: job.ID, UploadBPS: 1250000, DownloadBPS: 2500000}
	report("node-two", SpeedTestReport{Supported: true, Result: result})
	report("node-one", SpeedTestReport{Supported: true, Result: &SpeedTestResult{ID: "wrong-id"}})
	current, _ := s.NodeSpeedTest("node-one")
	if current.Status != "running" {
		t.Fatal("foreign result accepted")
	}
	report("node-one", SpeedTestReport{Supported: true, Result: result})
	current, _ = s.NodeSpeedTest("node-one")
	if current.Status != "completed" || current.UploadBPS != result.UploadBPS || current.DownloadBPS != result.DownloadBPS {
		t.Fatalf("missing result: %+v", current)
	}
	// A duplicate result cannot alter a terminal job.
	report("node-one", SpeedTestReport{Supported: true, Result: &SpeedTestResult{ID: job.ID, UploadBPS: 999}})
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return now }
	current, _ = s.NodeSpeedTest("node-one")
	if current.UploadBPS != result.UploadBPS {
		t.Fatal("result not persisted or duplicate overwritten")
	}
	if _, err := s.RequestSpeedTest("node-one"); !errors.Is(err, ErrSpeedTestBusy) {
		t.Fatal("cooldown not enforced", err)
	}
	current.Status = "changed"
	_, snapshot := s.Snapshot()
	if snapshot[0].SpeedTest.Status != "completed" {
		t.Fatal("result aliases store")
	}
	now = now.Add(61 * time.Second)
	if _, err := s.RequestSpeedTest("node-one"); !errors.Is(err, ErrNodeOffline) {
		t.Fatal(err)
	}
	report("node-one", SpeedTestReport{Supported: true})
	job, err = s.RequestSpeedTest("node-one")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(3 * time.Minute)
	current, _ = s.NodeSpeedTest("node-one")
	if current.Status != "failed" || current.Error == "" {
		t.Fatal("pending job did not expire")
	}
	report("node-one", SpeedTestReport{Supported: true, Result: &SpeedTestResult{ID: job.ID}})
	current, _ = s.NodeSpeedTest("node-one")
	if current.Status != "failed" {
		t.Fatal("late result revived expired job")
	}
}

func TestSpeedTestPartialFailureValidationAndSaveRollback(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatal(err)
	}
	report := func(result *SpeedTestResult) {
		t.Helper()
		if _, err := s.AutoReportCommands("speed-node", "", "192.0.2.1", "new", Metrics{}, true, SpeedTestReport{Supported: true, Result: result}); err != nil {
			t.Fatal(err)
		}
	}
	report(nil)
	job, err := s.RequestSpeedTest("speed-node")
	if err != nil {
		t.Fatal(err)
	}
	report(nil)
	report(&SpeedTestResult{ID: job.ID, UploadBPS: 10000000000001})
	view, _ := s.NodeSpeedTest("speed-node")
	if view.Status != "running" {
		t.Fatal("invalid rate accepted")
	}
	report(&SpeedTestResult{ID: job.ID, UploadError: "连接失败", DownloadBPS: 1000})
	view, _ = s.NodeSpeedTest("speed-node")
	if view.Status != "failed" || view.DownloadBPS != 1000 || view.UploadError == "" {
		t.Fatalf("partial result lost: %+v", view)
	}
	// A new store with a deliberately unwritable destination must not queue a job.
	s, err = Open(filepath.Join(t.TempDir(), "new.json"))
	if err != nil {
		t.Fatal(err)
	}
	report(nil)
	s.path = filepath.Join(t.TempDir(), "blocked")
	if err := os.Mkdir(s.path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RequestSpeedTest("speed-node"); err == nil {
		t.Fatal("expected save failure")
	}
	view, _ = s.NodeSpeedTest("speed-node")
	if view != nil {
		t.Fatal("failed save left a queued job")
	}
}
