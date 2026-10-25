package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/hainiu3/tz/internal/assets"
	"github.com/hainiu3/tz/internal/store"
)

var version = "dev"

type metrics struct {
	CPU           float64 `json:"cpu"`
	MemoryUsed    uint64  `json:"memoryUsed"`
	MemoryTotal   uint64  `json:"memoryTotal"`
	DiskUsed      uint64  `json:"diskUsed"`
	DiskTotal     uint64  `json:"diskTotal"`
	UploadSpeed   uint64  `json:"uploadSpeed"`
	DownloadSpeed uint64  `json:"downloadSpeed"`
	TotalUpload   uint64  `json:"totalUpload"`
	TotalDownload uint64  `json:"totalDownload"`
	Uptime        uint64  `json:"uptime"`
	BootID        string  `json:"bootId,omitempty"`
	NetworkSet    string  `json:"networkSet,omitempty"`
}

type cpuSample struct{ idle, total uint64 }
type netSample struct {
	tx, rx     uint64
	at         time.Time
	interfaces string
}

func main() {
	panel := flag.String("url", os.Getenv("TZ_PANEL_URL"), "面板地址")
	token := flag.String("token", os.Getenv("TZ_AGENT_TOKEN"), "探针令牌")
	nodeID := flag.String("id", os.Getenv("TZ_NODE_ID"), "节点唯一 ID")
	nodeName := flag.String("name", os.Getenv("TZ_NODE_NAME"), "节点名称")
	interval := flag.Duration("interval", 3*time.Second, "上报间隔")
	showVersion := flag.Bool("version", false, "显示版本")
	once := flag.Bool("once", false, "仅上报一次")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	if *panel == "" || *token == "" {
		log.Fatal("必须设置 --url 和 --token")
	}
	if *nodeID == "" {
		*nodeID = defaultNodeID()
	}
	if *nodeName == "" {
		*nodeName, _ = os.Hostname()
	}
	*panel = strings.TrimRight(*panel, "/")
	client := &http.Client{Timeout: 10 * time.Second}
	prevNet, _ := readNetwork()
	var lastUpgrade time.Time
	speedRunner := &speedTestRunner{}
	for {
		m, currentNet, err := collect(prevNet)
		if err != nil {
			log.Printf("采集失败：%v", err)
		} else {
			commands, err := report(client, *panel, *token, *nodeID, *nodeName, m, speedRunner.snapshot())
			if err != nil {
				log.Printf("上报失败：%v", err)
			} else if commands.Uninstall {
				if err := selfUninstall(); err != nil {
					log.Printf("安排卸载失败，将重试：%v", err)
				}
			} else if commands.Power != "" {
				if err := selfPower(commands.Power); err != nil {
					log.Printf("安排%s失败：%v", commands.Power, err)
				}
			} else if commands.Upgrade && time.Since(lastUpgrade) > time.Minute {
				lastUpgrade = time.Now()
				log.Printf("收到升级指令")
				go selfUpgrade(*panel, *token)
			} else if commands.SpeedTestID != "" {
				speedRunner.start(commands.SpeedTestID)
			}
			prevNet = currentNet
		}
		if *once {
			return
		}
		time.Sleep(*interval)
	}
}

func collect(prev netSample) (metrics, netSample, error) {
	var m metrics
	c1, err := readCPU()
	if err != nil {
		return m, prev, err
	}
	time.Sleep(220 * time.Millisecond)
	c2, err := readCPU()
	if err != nil {
		return m, prev, err
	}
	dTotal, dIdle := c2.total-c1.total, c2.idle-c1.idle
	if dTotal > 0 {
		m.CPU = float64(dTotal-dIdle) * 100 / float64(dTotal)
	}
	m.MemoryUsed, m.MemoryTotal, _ = readMemory()
	m.DiskUsed, m.DiskTotal, _ = readDisk("/")
	now, err := readNetwork()
	if err != nil {
		return m, prev, err
	}
	elapsed := now.at.Sub(prev.at).Seconds()
	if elapsed > 0 && now.interfaces == prev.interfaces && now.tx >= prev.tx && now.rx >= prev.rx {
		m.UploadSpeed = uint64(float64(now.tx-prev.tx) / elapsed)
		m.DownloadSpeed = uint64(float64(now.rx-prev.rx) / elapsed)
	}
	m.TotalUpload, m.TotalDownload = now.tx, now.rx
	m.Uptime = readUptime()
	boot, _ := os.ReadFile("/proc/sys/kernel/random/boot_id")
	m.BootID, m.NetworkSet = strings.TrimSpace(string(boot)), now.interfaces
	return m, now, nil
}

func readCPU() (cpuSample, error) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return cpuSample{}, err
	}
	f := strings.Fields(strings.SplitN(string(b), "\n", 2)[0])
	if len(f) < 5 {
		return cpuSample{}, errors.New("/proc/stat 格式无效")
	}
	var vals []uint64
	for _, v := range f[1:] {
		n, _ := strconv.ParseUint(v, 10, 64)
		vals = append(vals, n)
	}
	var total uint64
	for _, v := range vals {
		total += v
	}
	idle := vals[3]
	if len(vals) > 4 {
		idle += vals[4]
	}
	return cpuSample{idle: idle, total: total}, nil
}

func readMemory() (uint64, uint64, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	var total, available uint64
	s := bufio.NewScanner(f)
	for s.Scan() {
		p := strings.Fields(s.Text())
		if len(p) < 2 {
			continue
		}
		v, _ := strconv.ParseUint(p[1], 10, 64)
		if p[0] == "MemTotal:" {
			total = v * 1024
		}
		if p[0] == "MemAvailable:" {
			available = v * 1024
		}
	}
	return total - available, total, s.Err()
}

func readDisk(path string) (uint64, uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	total := st.Blocks * uint64(st.Bsize)
	free := st.Bavail * uint64(st.Bsize)
	return total - free, total, nil
}

func readNetwork() (netSample, error) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return netSample{}, err
	}
	defer f.Close()
	var rx, tx uint64
	var interfaces []string
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if !strings.Contains(line, ":") {
			continue
		}
		parts := strings.Fields(strings.NewReplacer(":", " ").Replace(line))
		if len(parts) < 17 || parts[0] == "lo" {
			continue
		}
		r, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil {
			return netSample{}, err
		}
		t, err := strconv.ParseUint(parts[9], 10, 64)
		if err != nil {
			return netSample{}, err
		}
		index, _ := os.ReadFile("/sys/class/net/" + parts[0] + "/ifindex")
		interfaces = append(interfaces, parts[0]+":"+strings.TrimSpace(string(index)))
		rx += r
		tx += t
	}
	sort.Strings(interfaces)
	return netSample{tx: tx, rx: rx, at: time.Now(), interfaces: strings.Join(interfaces, ",")}, s.Err()
}

func readUptime() uint64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	v, _ := strconv.ParseFloat(f[0], 64)
	return uint64(v)
}

func defaultNodeID() string {
	b, err := os.ReadFile("/etc/machine-id")
	if err != nil || len(bytes.TrimSpace(b)) == 0 {
		host, _ := os.Hostname()
		b = []byte(host)
	}
	sum := sha256.Sum256(bytes.TrimSpace(b))
	return hex.EncodeToString(sum[:16])
}

type agentCommands struct {
	Upgrade     bool   `json:"upgrade"`
	Uninstall   bool   `json:"uninstall"`
	Power       string `json:"power"`
	SpeedTestID string `json:"speedTestId"`
}

func report(client *http.Client, panel, token, nodeID, nodeName string, m metrics, speedResult ...*store.SpeedTestResult) (agentCommands, error) {
	var result *store.SpeedTestResult
	if len(speedResult) > 0 {
		result = speedResult[0]
	}
	payload := struct {
		NodeID             string                 `json:"nodeId"`
		Name               string                 `json:"name"`
		Version            string                 `json:"version"`
		UninstallSupported bool                   `json:"uninstallSupported"`
		PowerSupported     bool                   `json:"powerSupported"`
		SpeedTestSupported bool                   `json:"speedTestSupported"`
		SpeedTestResult    *store.SpeedTestResult `json:"speedTestResult,omitempty"`
		metrics
	}{nodeID, nodeName, version, true, true, true, result, m}
	b, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, panel+"/api/report", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Token", token)
	resp, err := client.Do(req)
	if err != nil {
		return agentCommands{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		text, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return agentCommands{}, fmt.Errorf("HTTP %d: %s", resp.StatusCode, text)
	}
	var out agentCommands
	err = json.NewDecoder(resp.Body).Decode(&out)
	return out, err
}

// A separate systemd unit survives stopping tz-agent and completes cleanup.
// The uninstall script is bundled locally, so execution needs no download.
func selfUninstall() error {
	script, err := assets.Files.ReadFile("scripts/uninstall-agent.sh")
	if err != nil {
		return err
	}
	cmd := exec.Command("systemd-run", "--unit=tz-agent-uninstall", "--collect", "bash", "-c", string(script))
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// Queue the action in a transient systemd timer so it survives the probe
// stopping as the host powers down. No arbitrary command text is accepted.
func selfPower(action string) error {
	command := ""
	switch action {
	case "reboot":
		command = "reboot"
	case "shutdown":
		command = "poweroff"
	default:
		return fmt.Errorf("unsupported power action %q", action)
	}
	unit := fmt.Sprintf("tz-agent-%s-%d", action, time.Now().UnixNano())
	cmd := exec.Command("systemd-run", "--unit="+unit, "--collect", "--on-active=3s", "--timer-property=AccuracySec=1s", "systemctl", command)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func selfUpgrade(panel, token string) {
	script := fmt.Sprintf("curl -fsSL %q/install.sh | bash -s -- --url %q --token %q --upgrade", panel, panel, token)
	unit := fmt.Sprintf("tz-agent-upgrade-%d", time.Now().Unix())
	cmd := exec.Command("systemd-run", "--unit="+unit, "--collect", "bash", "-c", script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Printf("升级失败：%v", err)
	}
}
