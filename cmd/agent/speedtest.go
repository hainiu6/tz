package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hainiu3/tz/internal/store"
)

// Fixed HTTPS endpoints: commands cannot supply URLs, scripts, or shell arguments.
const speedDownloadURL = "https://speed.cloudflare.com/__down"
const speedUploadURL = "https://speed.cloudflare.com/__up"
const speedByteLimit int64 = 64 * 1024 * 1024 // Per direction, at most 128 MiB total payload.

type speedTestRunner struct {
	mu     sync.Mutex
	busy   bool
	lastID string
	result *store.SpeedTestResult
}

func (runner *speedTestRunner) snapshot() *store.SpeedTestResult {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.result == nil {
		return nil
	}
	copy := *runner.result
	return &copy
}

func (runner *speedTestRunner) start(id string) {
	if decoded, err := hex.DecodeString(id); err != nil || len(decoded) != 12 {
		return
	}
	runner.mu.Lock()
	if runner.busy || runner.lastID == id {
		runner.mu.Unlock()
		return
	}
	runner.busy, runner.lastID, runner.result = true, id, nil
	runner.mu.Unlock()
	go func() {
		result := runSpeedTest(id)
		runner.mu.Lock()
		runner.result, runner.busy = &result, false
		runner.mu.Unlock()
	}()
}

func runSpeedTest(id string) store.SpeedTestResult {
	result := store.SpeedTestResult{ID: id}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	transport.MaxIdleConnsPerHost = 4
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 12 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return fmt.Errorf("测速端点发生重定向") }}
	// Random dummy data only; no files, credentials, or node metadata leave the node.
	payload := make([]byte, 512*1024)
	if _, err := rand.Read(payload); err != nil {
		result.UploadError, result.DownloadError = "生成测速数据失败", "生成测速数据失败"
		return result
	}
	for _, upload := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		rate, err := measureSpeed(ctx, client, upload, speedDownloadURL, speedUploadURL, speedByteLimit, 4, payload)
		cancel()
		message := ""
		if err != nil {
			message = "连接 Cloudflare 测速失败，请检查节点网络或稍后重试"
		}
		if upload {
			result.UploadBPS, result.UploadError = rate, message
		} else {
			result.DownloadBPS, result.DownloadError = rate, message
		}
	}
	return result
}

// measureSpeed measures aggregate HTTP payload throughput, including request/TLS
// overhead. It does not claim to measure the NIC's physical maximum line rate.
func measureSpeed(ctx context.Context, client *http.Client, upload bool, downURL, upURL string, limit int64, workers int, payload []byte) (uint64, error) {
	if limit <= 0 || workers < 1 || len(payload) == 0 {
		return 0, fmt.Errorf("invalid speed test limits")
	}
	started := time.Now()
	var reserved, transferred atomic.Int64
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error
	chunkSize := int64(4 * 1024 * 1024)
	if upload {
		chunkSize = int64(len(payload))
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				offset := reserved.Add(chunkSize) - chunkSize
				if offset >= limit {
					return
				}
				chunk := min(chunkSize, limit-offset)
				endpoint, method := fmt.Sprintf("%s?bytes=%d", downURL, chunk), http.MethodGet
				var body io.Reader
				if upload {
					endpoint, method, body = upURL, http.MethodPost, bytes.NewReader(payload[:int(chunk)])
				}
				req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
				if err == nil {
					req.Header.Set("Accept-Encoding", "identity")
					req.Header.Set("Cache-Control", "no-store")
					if upload {
						req.Header.Set("Content-Type", "application/octet-stream")
					}
					var resp *http.Response
					resp, err = client.Do(req)
					if err == nil {
						if resp.StatusCode < 200 || resp.StatusCode >= 300 {
							err = fmt.Errorf("speed endpoint HTTP %d", resp.StatusCode)
						} else if upload {
							_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 65536))
							if err == nil {
								transferred.Add(chunk)
							}
						} else {
							var n int64
							n, err = io.CopyN(io.Discard, resp.Body, chunk)
							if err == nil || ctx.Err() != nil {
								transferred.Add(n)
							}
						}
						resp.Body.Close()
					}
				}
				if err != nil {
					errMu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					errMu.Unlock()
					return
				}
			}
		}()
	}
	wg.Wait()
	count := transferred.Load()
	if count == 0 {
		if firstErr != nil {
			return 0, firstErr
		}
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, fmt.Errorf("no test data transferred")
	}
	return uint64(float64(count) / time.Since(started).Seconds()), nil
}
