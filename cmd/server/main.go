package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/hainiu6/tz/internal/assets"
	"github.com/hainiu6/tz/internal/downloads"
	"github.com/hainiu6/tz/internal/store"
)

var version = "dev"

type server struct {
	store       *store.Store
	adminToken  string
	agentToken  string
	tokenMu     sync.RWMutex
	ddnsMu      sync.Mutex
	ddnsRunning map[string]bool
}

func main() {
	addr := env("TZ_LISTEN", ":876")
	dataFile := env("TZ_DATA", "/var/lib/tz/data.json")
	initialAdminToken := os.Getenv("TZ_ADMIN_TOKEN")
	if initialAdminToken == "" {
		initialAdminToken = randomAdminToken()
		log.Printf("TZ_ADMIN_TOKEN 未设置，生成管理令牌：%s", initialAdminToken)
	}
	s, err := store.Open(dataFile)
	if err != nil {
		log.Fatal(err)
	}
	agentToken, err := s.EnsureEnrollmentToken(os.Getenv("TZ_AGENT_TOKEN"))
	if err != nil {
		log.Fatal(err)
	}
	adminToken, err := s.EnsureAdminToken(initialAdminToken)
	if err != nil {
		log.Fatal(err)
	}
	if err := persistAdminTokenEnv(os.Getenv("TZ_ENV_FILE"), adminToken); err != nil {
		log.Printf("同步管理令牌文件失败：%v", err)
	}
	app := &server{store: s, adminToken: adminToken, agentToken: agentToken}
	go app.runDDNSScheduler()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", app.api)
	mux.HandleFunc("/install.sh", installScript)
	mux.HandleFunc("/uninstall.sh", uninstallScript)
	mux.HandleFunc("/install-panel.sh", func(w http.ResponseWriter, r *http.Request) {
		serveScript(w, "scripts/install-panel.sh")
	})
	mux.Handle("/downloads/", downloadHandler(downloads.Files, env("TZ_DOWNLOAD_DIR", filepath.Join(filepath.Dir(dataFile), "downloads"))))
	web, _ := fs.Sub(assets.Files, "web")
	mux.Handle("/", http.FileServer(http.FS(web)))
	h := securityHeaders(requestLog(mux))
	log.Printf("TZ Panel %s 正在监听 %s", version, addr)
	log.Fatal(http.ListenAndServe(addr, h))
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func randomAdminToken() string {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("tz-%d", time.Now().UnixNano())
	}
	return "tz-" + hex.EncodeToString(b)
}

func installScript(w http.ResponseWriter, r *http.Request) {
	serveScript(w, "scripts/install-agent.sh")
}

func uninstallScript(w http.ResponseWriter, r *http.Request) {
	serveScript(w, "scripts/uninstall-agent.sh")
}

func serveScript(w http.ResponseWriter, path string) {
	b, err := assets.Files.ReadFile(path)
	if err != nil {
		http.Error(w, "installer unavailable", 500)
		return
	}
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Write(b)
}

func (s *server) api(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/"), "/")
	if path == "report" {
		s.report(w, r)
		return
	}
	if !s.authorized(r) {
		jsonError(w, "管理令牌无效", http.StatusUnauthorized)
		return
	}
	parts := strings.Split(path, "/")
	if parts[0] == "ddns" {
		s.ddnsAPI(w, r, parts)
		return
	}
	switch {
	case len(parts) == 3 && parts[0] == "nodes" && parts[2] == "speedtest":
		if r.Method == http.MethodGet {
			job, err := s.store.NodeSpeedTest(parts[1])
			if err != nil {
				respondErr(w, err)
				return
			}
			writeJSON(w, map[string]any{"speedTest": job})
		} else if r.Method == http.MethodPost {
			job, err := s.store.RequestSpeedTest(parts[1])
			switch {
			case errors.Is(err, store.ErrNodeOffline):
				jsonError(w, "服务器离线，无法测速", http.StatusConflict)
			case errors.Is(err, store.ErrSpeedTestUnsupported), errors.Is(err, store.ErrSpeedTestBusy):
				jsonError(w, err.Error(), http.StatusConflict)
			case errors.Is(err, os.ErrNotExist):
				respondErr(w, err)
			case err != nil:
				jsonError(w, "保存测速任务失败", http.StatusInternalServerError)
			default:
				writeJSON(w, map[string]any{"speedTest": job})
			}
		} else {
			jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	case path == "dashboard" && r.Method == http.MethodGet:
		groups, nodes := s.store.Snapshot()
		writeJSON(w, map[string]any{"groups": groups, "nodes": nodes, "serverTime": time.Now().UTC()})
	case path == "install" && r.Method == http.MethodGet:
		writeJSON(w, map[string]string{"agentToken": s.agentToken})
	case path == "admin-token" && r.Method == http.MethodPut:
		var in struct {
			Token string `json:"token"`
		}
		if !decode(w, r, &in) {
			return
		}
		if !validAdminToken(in.Token) {
			jsonError(w, "管理令牌不能为空", http.StatusBadRequest)
			return
		}
		if err := s.store.SetAdminToken(in.Token); err != nil {
			jsonError(w, "保存管理令牌失败", http.StatusInternalServerError)
			return
		}
		s.setAdminToken(in.Token)
		if err := persistAdminTokenEnv(os.Getenv("TZ_ENV_FILE"), in.Token); err != nil {
			log.Printf("同步管理令牌文件失败：%v", err)
		}
		writeJSON(w, map[string]bool{"ok": true})
	case path == "nodes" && r.Method == http.MethodPost:
		var in struct {
			Name, GroupID string
			Sort          int
		}
		if !decode(w, r, &in) {
			return
		}
		if strings.TrimSpace(in.Name) == "" {
			jsonError(w, "服务器名称不能为空", http.StatusBadRequest)
			return
		}
		n, err := s.store.CreateNode(strings.TrimSpace(in.Name), in.GroupID, in.Sort)
		if err != nil {
			jsonError(w, err.Error(), 500)
			return
		}
		publicNode := n
		publicNode.AgentToken = ""
		writeJSON(w, map[string]any{"node": publicNode, "agentToken": n.AgentToken})
	case len(parts) == 2 && parts[0] == "nodes" && r.Method == http.MethodPut:
		var in struct {
			Name, Note, GroupID string
			Sort                int
			GroupIDs            *[]string `json:"groupIds"`
			ExpiresAt           *string   `json:"expiresAt"`
			TrafficQuota        *string   `json:"trafficQuota"`
			TrafficQuotaUnit    *string   `json:"trafficQuotaUnit"`
			TrafficExhausted    *bool     `json:"trafficExhausted"`
		}
		if !decode(w, r, &in) {
			return
		}
		if strings.TrimSpace(in.Name) == "" {
			jsonError(w, "服务器名称不能为空", http.StatusBadRequest)
			return
		}
		note := strings.TrimSpace(in.Note)
		if utf8.RuneCountInString(note) > 500 {
			jsonError(w, "备注不能超过 500 个字符", http.StatusBadRequest)
			return
		}
		metadata := store.NodeMetadata{TrafficExhausted: in.TrafficExhausted}
		if in.ExpiresAt != nil {
			value := strings.TrimSpace(*in.ExpiresAt)
			if value != "" {
				parsed, err := time.Parse(time.RFC3339, value)
				if err != nil || parsed.Year() < 1 {
					jsonError(w, "到期时间格式无效", http.StatusBadRequest)
					return
				}
				value = parsed.UTC().Format(time.RFC3339)
			}
			metadata.ExpiresAt = &value
		}
		if in.TrafficQuota != nil {
			value, unit := strings.TrimSpace(*in.TrafficQuota), ""
			if value != "" {
				amount, err := strconv.ParseFloat(value, 64)
				if err != nil || math.IsNaN(amount) || math.IsInf(amount, 0) || amount < 0 {
					jsonError(w, "流量必须为大于或等于 0 的有效数值", http.StatusBadRequest)
					return
				}
				value = strconv.FormatFloat(amount, 'f', -1, 64)
				unit = "TB"
				if in.TrafficQuotaUnit != nil && strings.TrimSpace(*in.TrafficQuotaUnit) != "" {
					unit = strings.ToUpper(strings.TrimSpace(*in.TrafficQuotaUnit))
				}
				switch unit {
				case "B", "KB", "MB", "GB", "TB", "PB":
				default:
					jsonError(w, "流量单位只支持 B、KB、MB、GB、TB、PB", http.StatusBadRequest)
					return
				}
			}
			metadata.TrafficQuota, metadata.TrafficQuotaUnit = &value, &unit
		} else if in.TrafficQuotaUnit != nil {
			jsonError(w, "请同时填写流量数值和单位", http.StatusBadRequest)
			return
		}
		groupIDs := []string{}
		if in.GroupIDs != nil {
			groupIDs = *in.GroupIDs
		} else if in.GroupID != "" {
			groupIDs = []string{in.GroupID}
		}
		respondErr(w, s.store.UpdateNode(parts[1], strings.TrimSpace(in.Name), note, groupIDs, in.Sort, metadata))
	case len(parts) == 2 && parts[0] == "nodes" && r.Method == http.MethodDelete:
		respondErr(w, s.store.DeleteNode(parts[1]))
	case len(parts) == 3 && parts[0] == "nodes" && parts[2] == "upgrade" && r.Method == http.MethodPost:
		respondErr(w, s.store.RequestUpgrade(parts[1]))
	case len(parts) == 3 && parts[0] == "nodes" && parts[2] == "reset-traffic" && r.Method == http.MethodPost:
		err := s.store.ResetNodeTraffic(parts[1])
		if errors.Is(err, os.ErrNotExist) {
			jsonError(w, "节点不存在", 404)
		} else if err != nil {
			jsonError(w, "清空流量失败", 500)
		} else {
			writeJSON(w, map[string]bool{"ok": true})
		}
	case path == "nodes/upgrade-all" && r.Method == http.MethodPost:
		count, err := s.store.RequestUpgradeAll()
		if err != nil {
			jsonError(w, "发送升级指令失败", 500)
			return
		}
		writeJSON(w, map[string]int{"count": count})
	case len(parts) == 3 && parts[0] == "nodes" && parts[2] == "power" && r.Method == http.MethodPost:
		var in struct {
			Action string `json:"action"`
		}
		if !decode(w, r, &in) {
			return
		}
		err := s.store.RequestPower(parts[1], in.Action)
		switch {
		case err == nil:
			writeJSON(w, map[string]bool{"ok": true})
		case errors.Is(err, store.ErrInvalidPowerAction):
			jsonError(w, "只允许重启或关机", 400)
		case errors.Is(err, store.ErrNodeOffline):
			jsonError(w, "节点已离线，未发送指令", 409)
		case errors.Is(err, store.ErrPowerPending):
			jsonError(w, "已有待执行的重启或关机指令", 409)
		case errors.Is(err, os.ErrNotExist):
			jsonError(w, "节点不存在", 404)
		default:
			jsonError(w, "发送指令失败", 500)
		}
	case path == "groups" && r.Method == http.MethodPost:
		var in struct {
			Name string
			Sort int
		}
		if !decode(w, r, &in) {
			return
		}
		if strings.TrimSpace(in.Name) == "" {
			jsonError(w, "分组名称不能为空", http.StatusBadRequest)
			return
		}
		g, err := s.store.CreateGroup(strings.TrimSpace(in.Name), in.Sort)
		if err != nil {
			jsonError(w, err.Error(), 500)
			return
		}
		writeJSON(w, g)
	case len(parts) == 2 && parts[0] == "groups" && r.Method == http.MethodPut:
		var in struct {
			Name    string
			Sort    int
			NodeIDs *[]string `json:"nodeIds"`
		}
		if !decode(w, r, &in) {
			return
		}
		if strings.TrimSpace(in.Name) == "" {
			jsonError(w, "分组名称不能为空", http.StatusBadRequest)
			return
		}
		if in.NodeIDs == nil {
			respondErr(w, s.store.UpdateGroup(parts[1], strings.TrimSpace(in.Name), in.Sort))
		} else {
			respondErr(w, s.store.UpdateGroupWithNodes(parts[1], strings.TrimSpace(in.Name), in.Sort, *in.NodeIDs))
		}
	case len(parts) == 2 && parts[0] == "groups" && r.Method == http.MethodDelete:
		respondErr(w, s.store.DeleteGroup(parts[1]))
	default:
		jsonError(w, "接口不存在", http.StatusNotFound)
	}
}

func (s *server) report(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", 405)
		return
	}
	token := r.Header.Get("X-Agent-Token")
	var in struct {
		NodeID             string                 `json:"nodeId"`
		Name               string                 `json:"name"`
		Version            string                 `json:"version"`
		UninstallSupported bool                   `json:"uninstallSupported"`
		PowerSupported     bool                   `json:"powerSupported"`
		SpeedTestSupported bool                   `json:"speedTestSupported"`
		SpeedTestResult    *store.SpeedTestResult `json:"speedTestResult"`
		store.Metrics
	}
	if !decode(w, r, &in) {
		return
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); forwarded != "" {
		host = forwarded
	}
	var commands store.NodeCommands
	if token == s.agentToken && validNodeID(in.NodeID) {
		name := strings.TrimSpace(in.Name)
		if name == "" {
			name = "Linux 节点"
		}
		if len(name) > 60 {
			name = name[:60]
		}
		commands, err = s.store.AutoReportCommands(in.NodeID, name, host, in.Version, in.Metrics, in.PowerSupported, store.SpeedTestReport{Supported: in.SpeedTestSupported, Result: in.SpeedTestResult})
	} else {
		// Keep existing per-node tokens working during migration.
		commands, err = s.store.ReportCommands(token, host, in.Version, in.Metrics, in.PowerSupported, store.SpeedTestReport{Supported: in.SpeedTestSupported, Result: in.SpeedTestResult})
	}
	if errors.Is(err, store.ErrRemovalPending) {
		writeJSON(w, map[string]any{"ok": true, "uninstall": in.UninstallSupported, "upgrade": !in.UninstallSupported})
		return
	}
	if err != nil {
		jsonError(w, "探针令牌无效", http.StatusUnauthorized)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "upgrade": commands.Upgrade, "power": commands.Power, "speedTestId": commands.SpeedTestID})
}

func validNodeID(id string) bool {
	if len(id) < 8 || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

func validAdminToken(value string) bool {
	return value != ""
}

func (s *server) authorized(r *http.Request) bool {
	s.tokenMu.RLock()
	defer s.tokenMu.RUnlock()
	if encoded := r.Header.Get("X-TZ-Admin-Token"); encoded != "" {
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		return err == nil && s.adminToken != "" && string(decoded) == s.adminToken
	}
	return s.adminToken != "" && strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") == s.adminToken
}

func (s *server) setAdminToken(value string) {
	s.tokenMu.Lock()
	s.adminToken = value
	s.tokenMu.Unlock()
}

func persistAdminTokenEnv(path, token string) error {
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	assignment := regexp.MustCompile(`(?m)^TZ_ADMIN_TOKEN=(?:"(?:\\[\s\S]|[^"\\])*"[^\n]*|[^\n]*)(?:\n|$)`)
	found := false
	content := assignment.ReplaceAllStringFunc(string(b), func(string) string {
		if found {
			return ""
		}
		found = true
		return "TZ_ADMIN_TOKEN=" + quoteEnvToken(token) + "\n"
	})
	if !found {
		content = strings.TrimRight(content, "\n") + "\nTZ_ADMIN_TOKEN=" + quoteEnvToken(token) + "\n"
	}
	return os.WriteFile(path, []byte(content), 0600)
}

func quoteEnvToken(token string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, "`", "\\`").Replace(token) + `"`
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		jsonError(w, "请求内容无效", 400)
		return false
	}
	return true
}

func respondErr(w http.ResponseWriter, err error) {
	if err != nil {
		jsonError(w, "记录不存在", 404)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
func jsonError(w http.ResponseWriter, message string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	writeJSON(w, map[string]string{"error": message})
}

func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(w, r) })
}
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; connect-src 'self'")
		if r.URL.Path == "/" || strings.HasSuffix(r.URL.Path, ".js") || strings.HasSuffix(r.URL.Path, ".css") {
			w.Header().Set("Cache-Control", "no-cache")
		}
		next.ServeHTTP(w, r)
	})
}
