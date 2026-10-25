package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReportReceivesUninstall(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			NodeID             string
			UninstallSupported bool
			PowerSupported     bool
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
		}
		if in.NodeID != "test-node" || !in.UninstallSupported || !in.PowerSupported || r.Header.Get("X-Agent-Token") != "test-token" {
			t.Error("missing node identity or uninstall capability")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true,"uninstall":true,"upgrade":false}`))
	}))
	defer s.Close()
	commands, err := report(s.Client(), s.URL, "test-token", "test-node", "", metrics{})
	if err != nil || !commands.Uninstall || commands.Upgrade {
		t.Fatalf("commands=%+v error=%v", commands, err)
	}
}

func TestPowerUsesAllowlistedDetachedCommand(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "arguments")
	fake := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$TZ_TEST_CAPTURE\"\n"
	if err := os.WriteFile(filepath.Join(dir, "systemd-run"), []byte(fake), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("TZ_TEST_CAPTURE", capture)
	for _, tc := range []struct{ action, expected string }{{"reboot", "reboot"}, {"shutdown", "poweroff"}} {
		if err := selfPower(tc.action); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(capture)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"--unit=tz-agent-" + tc.action, "--collect", "--on-active=3s", "--timer-property=AccuracySec=1s", "systemctl\n" + tc.expected} {
			if !strings.Contains(string(b), want) {
				t.Fatalf("%s missing %q: %s", tc.action, want, b)
			}
		}
	}
	if err := selfPower("rm -rf /"); err == nil {
		t.Fatal("unexpected command accepted")
	}
}

func TestUninstallSchedulesSeparateUnit(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "arguments")
	// Capture arguments only. Never execute the real uninstall script in tests.
	fake := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$TZ_TEST_CAPTURE\"\n"
	if err := os.WriteFile(filepath.Join(dir, "systemd-run"), []byte(fake), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("TZ_TEST_CAPTURE", capture)
	if err := selfUninstall(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--unit=tz-agent-uninstall", "--collect", "systemctl disable --now tz-agent", "/usr/local/bin/tz-agent /etc/tz-agent.env"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %q", want)
		}
	}
}
