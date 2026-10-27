package main

import (
	"errors"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/hainiu6/tz/internal/store"
)

var dnsNamePattern = regexp.MustCompile(`(?i)^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func normalizeDDNSTask(task *store.DDNSTask) string {
	task.Domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(task.Domain), "."))
	task.Target = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(task.Target), "."))
	task.Type = strings.ToUpper(strings.TrimSpace(task.Type))
	if task.Type == "" {
		task.Type = "A"
	}
	if len(task.Domain) > 253 || !dnsNamePattern.MatchString(task.Domain) {
		return "解析域名格式无效"
	}
	if task.Type != "A" && task.Type != "CNAME" {
		return "记录类型只能是 A 或 CNAME"
	}
	if task.Type == "A" {
		ip := net.ParseIP(task.Target)
		if ip == nil || ip.To4() == nil || task.Target != ip.To4().String() {
			return "A 记录目标必须是 IPv4 地址"
		}
	} else if len(task.Target) > 253 || !dnsNamePattern.MatchString(task.Target) {
		return "CNAME 目标必须是域名"
	}
	if task.NextRun.IsZero() || task.NextRun.Year() < 2020 || task.NextRun.Year() > 2100 {
		return "请选择执行日期和时间"
	}
	if task.Repeat == "" {
		task.Repeat = "once"
	}
	task.RunDay = task.NextRun.In(time.FixedZone("CST", 8*3600)).Day()
	switch task.Repeat {
	case "once", "daily", "weekly", "monthly":
	default:
		return "重复方式无效"
	}
	return ""
}

func (s *server) ddnsAPI(w http.ResponseWriter, r *http.Request, parts []string) {
	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		ak, sk := s.store.DDNSCredentials()
		writeJSON(w, map[string]any{"configured": ak != "" && sk != "", "tasks": s.store.DDNSTasks()})
	case len(parts) == 2 && parts[1] == "credentials" && r.Method == http.MethodPut:
		var in struct {
			AccessKeyID     string `json:"accessKeyId"`
			SecretAccessKey string `json:"secretAccessKey"`
		}
		if !decode(w, r, &in) {
			return
		}
		in.AccessKeyID = strings.TrimSpace(in.AccessKeyID)
		in.SecretAccessKey = strings.TrimSpace(in.SecretAccessKey)
		if in.AccessKeyID == "" || in.SecretAccessKey == "" {
			jsonError(w, "请填写 Access Key Id 和 Secret Access Key", 400)
			return
		}
		if len(in.AccessKeyID) > 256 || len(in.SecretAccessKey) > 256 {
			jsonError(w, "密钥长度不能超过 256 字符", 400)
			return
		}
		if err := s.store.SetDDNSCredentials(in.AccessKeyID, in.SecretAccessKey); err != nil {
			jsonError(w, "保存密钥失败", 500)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	case len(parts) == 2 && parts[1] == "tasks" && r.Method == http.MethodPost:
		var task store.DDNSTask
		if !decode(w, r, &task) {
			return
		}
		if message := normalizeDDNSTask(&task); message != "" {
			jsonError(w, message, 400)
			return
		}
		created, err := s.store.CreateDDNSTask(task)
		if err != nil {
			jsonError(w, "保存任务失败", 500)
			return
		}
		writeJSON(w, created)
	case len(parts) == 3 && parts[1] == "tasks" && r.Method == http.MethodPut:
		var task store.DDNSTask
		if !decode(w, r, &task) {
			return
		}
		if message := normalizeDDNSTask(&task); message != "" {
			jsonError(w, message, 400)
			return
		}
		if err := s.store.UpdateDDNSTask(parts[2], task); err != nil {
			ddnsStoreError(w, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	case len(parts) == 3 && parts[1] == "tasks" && r.Method == http.MethodDelete:
		s.ddnsMu.Lock()
		if s.ddnsRunning[parts[2]] {
			s.ddnsMu.Unlock()
			jsonError(w, "任务正在执行，请稍后删除", 409)
			return
		}
		err := s.store.DeleteDDNSTask(parts[2])
		s.ddnsMu.Unlock()
		if err != nil {
			ddnsStoreError(w, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	case len(parts) == 4 && parts[1] == "tasks" && parts[3] == "enabled" && r.Method == http.MethodPut:
		var in struct {
			Enabled *bool `json:"enabled"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Enabled == nil {
			jsonError(w, "缺少启用状态", 400)
			return
		}
		if err := s.store.SetDDNSTaskEnabled(parts[2], *in.Enabled); err != nil {
			ddnsStoreError(w, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	case len(parts) == 4 && parts[1] == "tasks" && parts[3] == "logs" && r.Method == http.MethodGet:
		logs, err := s.store.DDNSLogs(parts[2])
		if err != nil {
			ddnsStoreError(w, err)
			return
		}
		writeJSON(w, map[string]any{"logs": logs})
	case len(parts) == 4 && parts[1] == "tasks" && parts[3] == "run" && r.Method == http.MethodPost:
		entry, err := s.runDDNSTask(parts[2], false)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				jsonError(w, "任务不存在", 404)
			} else if err.Error() == "running" {
				jsonError(w, "任务正在执行", 409)
			} else {
				jsonError(w, "无法执行任务", 500)
			}
			return
		}
		writeJSON(w, map[string]any{"log": entry})
	default:
		jsonError(w, "接口不存在", 404)
	}
}

func ddnsStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, os.ErrNotExist) {
		jsonError(w, "任务不存在", 404)
	} else {
		jsonError(w, "保存任务失败", 500)
	}
}

func (s *server) runDDNSTask(id string, scheduled bool) (store.DDNSLog, error) {
	s.ddnsMu.Lock()
	if s.ddnsRunning == nil {
		s.ddnsRunning = make(map[string]bool)
	}
	if s.ddnsRunning[id] {
		s.ddnsMu.Unlock()
		return store.DDNSLog{}, errors.New("running")
	}
	s.ddnsRunning[id] = true
	s.ddnsMu.Unlock()
	defer func() { s.ddnsMu.Lock(); delete(s.ddnsRunning, id); s.ddnsMu.Unlock() }()

	task, err := s.store.BeginDDNSRun(id, scheduled, time.Now())
	if err != nil {
		return store.DDNSLog{}, err
	}
	trigger := "manual"
	if scheduled {
		trigger = "scheduled"
	}
	entry := store.DDNSLog{At: time.Now().UTC(), Trigger: trigger, Status: "failed", To: task.Target}
	ak, sk := s.store.DDNSCredentials()
	if ak == "" || sk == "" {
		entry.Message = "尚未设置华为云密钥"
	} else {
		client, clientErr := newHuaweiDNSClient(ak, sk)
		if clientErr != nil {
			entry.Message = "初始化华为云客户端失败"
		} else {
			old, status, updateErr := updateHuaweiRecord(client, task)
			entry.From = old
			if updateErr != nil {
				entry.Message = strings.ReplaceAll(strings.ReplaceAll(updateErr.Error(), ak, "[已隐藏]"), sk, "[已隐藏]")
				if len(entry.Message) > 500 {
					entry.Message = entry.Message[:500]
				}
			} else {
				entry.Status = status
			}
		}
	}
	if err := s.store.CompleteDDNSRun(id, entry); err != nil {
		return entry, err
	}
	return entry, nil
}

func (s *server) runDDNSScheduler() {
	check := func() {
		for _, task := range s.store.DueDDNSTasks(time.Now()) {
			go func(id string) { _, _ = s.runDDNSTask(id, true) }(task.ID)
		}
	}
	check()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		check()
	}
}
