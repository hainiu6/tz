package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hainiu3/tz/internal/store"
)

func TestMeasureSpeedBoundedUploadAndDownload(t *testing.T) {
	var uploaded, downloaded atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept-Encoding") != "identity" {
			t.Error("compression must be disabled")
		}
		switch r.URL.Path {
		case "/down":
			if r.Method != http.MethodGet {
				t.Error("wrong download method")
			}
			n, _ := strconv.Atoi(r.URL.Query().Get("bytes"))
			downloaded.Add(int64(n))
			w.Write(bytes.Repeat([]byte("d"), n))
		case "/up":
			if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/octet-stream" {
				t.Error("wrong upload request")
			}
			n, err := io.Copy(io.Discard, r.Body)
			if err != nil {
				t.Error(err)
			}
			uploaded.Add(n)
			w.Write([]byte("ok"))
		default:
			t.Error("unexpected endpoint")
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	const limit = 65537 // Also exercise a final partial chunk.
	for _, upload := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		rate, err := measureSpeed(ctx, server.Client(), upload, server.URL+"/down", server.URL+"/up", limit, 4, make([]byte, 4096))
		cancel()
		if err != nil || rate == 0 {
			t.Fatalf("upload=%v rate=%d err=%v", upload, rate, err)
		}
	}
	if uploaded.Load() != limit || downloaded.Load() != limit {
		t.Fatalf("unbounded traffic: up=%d down=%d", uploaded.Load(), downloaded.Load())
	}
}

func TestMeasureSpeedFailuresDoNotBecomeResults(t *testing.T) {
	for _, mode := range []string{"forbidden", "truncated", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "forbidden":
					w.WriteHeader(403)
				case "truncated":
					w.Write([]byte("short"))
				case "timeout":
					<-r.Context().Done()
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			rate, err := measureSpeed(ctx, server.Client(), false, server.URL, server.URL, 1024, 1, []byte("dummy"))
			if err == nil || rate != 0 {
				t.Fatalf("false success: rate=%d err=%v", rate, err)
			}
		})
	}
}

func TestReportCarriesSpeedTestCapabilityResultAndCommand(t *testing.T) {
	result := &store.SpeedTestResult{ID: "0123456789abcdef01234567", UploadBPS: 100, DownloadBPS: 200}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			SpeedTestSupported bool                   `json:"speedTestSupported"`
			SpeedTestResult    *store.SpeedTestResult `json:"speedTestResult"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
		}
		if !in.SpeedTestSupported || in.SpeedTestResult == nil || *in.SpeedTestResult != *result {
			t.Errorf("missing capability or result: %+v", in)
		}
		json.NewEncoder(w).Encode(map[string]any{"speedTestId": result.ID})
	}))
	defer server.Close()
	command, err := report(server.Client(), server.URL, "token", "node", "name", metrics{}, result)
	if err != nil || command.SpeedTestID != result.ID {
		t.Fatalf("command=%+v err=%v", command, err)
	}
}

func TestSpeedRunnerRejectsInvalidBusyAndDuplicateCommands(t *testing.T) {
	id := "0123456789abcdef01234567"
	runner := &speedTestRunner{busy: true, lastID: id}
	runner.start("https://example.com/run.sh")
	runner.start("abcdef0123456789abcdef01")
	runner.busy = false
	runner.start(id)
	if runner.busy || runner.lastID != id {
		t.Fatal("duplicate or busy job started")
	}
	runner.result = &store.SpeedTestResult{ID: id, UploadBPS: 10}
	copy := runner.snapshot()
	copy.UploadBPS = 999
	if runner.snapshot().UploadBPS != 10 {
		t.Fatal("result snapshot aliases runner state")
	}
}
