package store

import (
	"errors"
	"os"
	"time"
)

// SpeedTestResult is reported only by the node that received the matching job ID.
type SpeedTestResult struct {
	ID            string `json:"id"`
	UploadBPS     uint64 `json:"uploadBps"`
	DownloadBPS   uint64 `json:"downloadBps"`
	UploadError   string `json:"uploadError,omitempty"`
	DownloadError string `json:"downloadError,omitempty"`
}

type SpeedTest struct {
	SpeedTestResult
	Status      string    `json:"status"`
	RequestedAt time.Time `json:"requestedAt"`
	CompletedAt time.Time `json:"completedAt,omitempty"`
	Error       string    `json:"error,omitempty"`
}

type SpeedTestReport struct {
	Supported bool
	Result    *SpeedTestResult
}

var ErrSpeedTestUnsupported = errors.New("请先升级这台服务器的探针后再测速")
var ErrSpeedTestBusy = errors.New("测速进行中或请求过于频繁，请稍后再试")

func speedTestView(job *SpeedTest, now time.Time) *SpeedTest {
	if job == nil {
		return nil
	}
	copy := *job
	if (copy.Status == "pending" || copy.Status == "running") && now.Sub(copy.RequestedAt) > 2*time.Minute {
		copy.Status, copy.Error, copy.CompletedAt = "failed", "测速超时，节点可能已离线或重启，请重试", copy.RequestedAt.Add(2*time.Minute)
	}
	return &copy
}

func (n *Node) applySpeedTestReport(reports []SpeedTestReport, now time.Time) {
	n.SpeedTest = speedTestView(n.SpeedTest, now)
	if len(reports) == 0 {
		return
	}
	report := reports[0]
	n.SpeedTestSupported = report.Supported
	result := report.Result
	if result == nil || n.SpeedTest == nil || n.SpeedTest.Status != "running" || result.ID != n.SpeedTest.ID {
		return
	}
	if result.UploadBPS > 1e13 || result.DownloadBPS > 1e13 || len(result.UploadError) > 240 || len(result.DownloadError) > 240 {
		return
	}
	job := *n.SpeedTest
	job.SpeedTestResult, job.Status, job.CompletedAt = *result, "completed", now
	if result.UploadError != "" || result.DownloadError != "" {
		job.Status = "failed"
	}
	n.SpeedTest = &job
}

func (s *Store) RequestSpeedTest(id string) (*SpeedTest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for i := range s.data.Nodes {
		n := &s.data.Nodes[i]
		if n.ID != id {
			continue
		}
		if n.LastSeen.IsZero() || now.Sub(n.LastSeen) >= 15*time.Second {
			return nil, ErrNodeOffline
		}
		if !n.SpeedTestSupported {
			return nil, ErrSpeedTestUnsupported
		}
		job := speedTestView(n.SpeedTest, now)
		if job != nil && (job.Status == "pending" || job.Status == "running" || now.Sub(job.RequestedAt) < time.Minute) {
			return nil, ErrSpeedTestBusy
		}
		previous := n.SpeedTest
		n.SpeedTest = &SpeedTest{SpeedTestResult: SpeedTestResult{ID: token(12)}, Status: "pending", RequestedAt: now}
		if err := s.saveLocked(); err != nil {
			n.SpeedTest = previous
			return nil, err
		}
		return speedTestView(n.SpeedTest, now), nil
	}
	return nil, os.ErrNotExist
}

func (s *Store) NodeSpeedTest(id string) (*SpeedTest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, node := range s.data.Nodes {
		if node.ID == id {
			return speedTestView(node.SpeedTest, s.now()), nil
		}
	}
	return nil, os.ErrNotExist
}
