package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type Metrics struct {
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

type Node struct {
	ID                         string    `json:"id"`
	Name                       string    `json:"name"`
	Note                       string    `json:"note,omitempty"`
	ExpiresAt                  string    `json:"expiresAt,omitempty"`
	IP                         string    `json:"ip"`
	GroupIDs                   []string  `json:"groupIds"`
	GroupID                    string    `json:"groupId,omitempty"` // legacy API compatibility
	Sort                       int       `json:"sort"`
	AgentToken                 string    `json:"agentToken,omitempty"`
	Version                    string    `json:"version"`
	LastSeen                   time.Time `json:"lastSeen"`
	UpgradeRequested           bool      `json:"upgradeRequested,omitempty"`
	PowerRequested             string    `json:"powerRequested,omitempty"`
	PowerRequestedAt           time.Time `json:"powerRequestedAt,omitempty"`
	TrafficDate                string    `json:"trafficDate"`
	TodayUpload                uint64    `json:"todayUpload"`
	TodayDownload              uint64    `json:"todayDownload"`
	YesterdayUpload            uint64    `json:"yesterdayUpload"`
	YesterdayDownload          uint64    `json:"yesterdayDownload"`
	DayBeforeYesterdayUpload   uint64    `json:"dayBeforeYesterdayUpload"`
	DayBeforeYesterdayDownload uint64    `json:"dayBeforeYesterdayDownload"`
	UploadBaseline             uint64    `json:"uploadBaseline"`
	DownloadBaseline           uint64    `json:"downloadBaseline"`
	BaselineReady              bool      `json:"baselineReady"`
	TrafficResetAt             time.Time `json:"trafficResetAt,omitempty"`
	TrafficResetPending        bool      `json:"trafficResetPending,omitempty"`
	UploadSinceReset           uint64    `json:"uploadSinceReset,omitempty"`
	DownloadSinceReset         uint64    `json:"downloadSinceReset,omitempty"`
	Metrics
}

type Group struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Sort int    `json:"sort"`
}

type data struct {
	EnrollmentToken     string               `json:"enrollmentToken"`
	AdminToken          string               `json:"adminToken"`
	Groups              []Group              `json:"groups"`
	Nodes               []Node               `json:"nodes"`
	RemovedNodes        []RemovedNode        `json:"removedNodes,omitempty"`
	DDNSAccessKeyID     string               `json:"ddnsAccessKeyId,omitempty"`
	DDNSSecretAccessKey string               `json:"ddnsSecretAccessKey,omitempty"`
	DDNSTasks           []DDNSTask           `json:"ddnsTasks,omitempty"`
	DDNSLogs            map[string][]DDNSLog `json:"ddnsLogs,omitempty"`
}

type RemovedNode struct {
	ID         string `json:"id"`
	AgentToken string `json:"agentToken,omitempty"`
}

var ErrRemovalPending = errors.New("agent removal pending")

// EnsureAdminToken stores the initial token and returns the persisted token.
// A token changed in the dashboard therefore survives service restarts.
func (s *Store) EnsureAdminToken(initial string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.AdminToken == "" {
		s.data.AdminToken = initial
	}
	return s.data.AdminToken, s.saveLocked()
}

func (s *Store) SetAdminToken(value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.AdminToken = value
	return s.saveLocked()
}

// EnsureEnrollmentToken returns the shared token used by all new agents. The
// token is stored with panel data so it remains stable across restarts.
func (s *Store) EnsureEnrollmentToken(configured string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if configured != "" {
		s.data.EnrollmentToken = configured
	}
	if s.data.EnrollmentToken == "" {
		s.data.EnrollmentToken = token(24)
	}
	return s.data.EnrollmentToken, s.saveLocked()
}

type Store struct {
	mu   sync.RWMutex
	path string
	data data
	now  func() time.Time
}

func Open(path string) (*Store, error) {
	s := &Store{path: path, data: data{Groups: []Group{}, Nodes: []Node{}}, now: time.Now}
	b, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(b, &s.data); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for i := range s.data.Nodes {
		if len(s.data.Nodes[i].GroupIDs) == 0 && s.data.Nodes[i].GroupID != "" {
			s.data.Nodes[i].GroupIDs = []string{s.data.Nodes[i].GroupID}
		}
		if s.data.Nodes[i].GroupIDs == nil {
			s.data.Nodes[i].GroupIDs = []string{}
		}
		s.data.Nodes[i].GroupID = ""
	}
	return s, nil
}

func token(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) Snapshot() ([]Group, []Node) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	groups := append([]Group{}, s.data.Groups...)
	nodes := append([]Node{}, s.data.Nodes...)
	now := s.now()
	for i := range nodes {
		nodes[i].rollTrafficDay(now)
		if !nodes[i].TrafficResetAt.IsZero() {
			nodes[i].TotalUpload, nodes[i].TotalDownload = nodes[i].UploadSinceReset, nodes[i].DownloadSinceReset
		}
		nodes[i].AgentToken = ""
		nodes[i].UpgradeRequested = false
		if len(nodes[i].GroupIDs) > 0 {
			nodes[i].GroupID = nodes[i].GroupIDs[0]
		}
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].Sort > groups[j].Sort })
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].Sort == nodes[j].Sort {
			return false
		}
		return nodes[i].Sort > nodes[j].Sort
	})
	return groups, nodes
}

func (s *Store) CreateNode(name, groupID string, order int) (Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	groupIDs := []string{}
	if groupID != "" {
		groupIDs = append(groupIDs, groupID)
	}
	n := Node{ID: token(8), Name: name, GroupIDs: groupIDs, Sort: order, AgentToken: token(24)}
	s.data.Nodes = append(s.data.Nodes, n)
	return n, s.saveLocked()
}

// Omitting expiresAt preserves the previous expiry for older API clients.
// Passing an empty string explicitly clears it.
func (s *Store) UpdateNode(id, name, note string, groupIDs []string, order int, expiresAt ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Nodes {
		if s.data.Nodes[i].ID == id {
			previous := s.data.Nodes[i]
			s.data.Nodes[i].Name, s.data.Nodes[i].Note = name, note
			s.data.Nodes[i].GroupIDs, s.data.Nodes[i].Sort = unique(groupIDs), order
			s.data.Nodes[i].GroupID = ""
			if len(expiresAt) > 0 {
				s.data.Nodes[i].ExpiresAt = expiresAt[0]
			}
			if err := s.saveLocked(); err != nil {
				s.data.Nodes[i] = previous
				return err
			}
			return nil
		}
	}
	return os.ErrNotExist
}

func (s *Store) DeleteNode(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Nodes {
		if s.data.Nodes[i].ID == id {
			oldNodes := append([]Node{}, s.data.Nodes...)
			oldRemoved := s.data.RemovedNodes
			s.data.RemovedNodes = append(s.data.RemovedNodes, RemovedNode{ID: id, AgentToken: s.data.Nodes[i].AgentToken})
			s.data.Nodes = append(s.data.Nodes[:i], s.data.Nodes[i+1:]...)
			if err := s.saveLocked(); err != nil {
				s.data.Nodes, s.data.RemovedNodes = oldNodes, oldRemoved
				return err
			}
			return nil
		}
	}
	return os.ErrNotExist
}

func (s *Store) CreateGroup(name string, order int) (Group, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := Group{ID: token(6), Name: name, Sort: order}
	s.data.Groups = append(s.data.Groups, g)
	return g, s.saveLocked()
}

func (s *Store) UpdateGroup(id, name string, order int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Groups {
		if s.data.Groups[i].ID == id {
			s.data.Groups[i].Name, s.data.Groups[i].Sort = name, order
			return s.saveLocked()
		}
	}
	return os.ErrNotExist
}

// UpdateGroupWithNodes updates membership for this group without changing a
// node's membership in any other group.
func (s *Store) UpdateGroupWithNodes(id, name string, order int, nodeIDs []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	found := false
	for i := range s.data.Groups {
		if s.data.Groups[i].ID == id {
			s.data.Groups[i].Name, s.data.Groups[i].Sort = name, order
			found = true
			break
		}
	}
	if !found {
		return os.ErrNotExist
	}
	selected := make(map[string]bool, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		selected[nodeID] = true
	}
	for i := range s.data.Nodes {
		if selected[s.data.Nodes[i].ID] {
			if !contains(s.data.Nodes[i].GroupIDs, id) {
				s.data.Nodes[i].GroupIDs = append(s.data.Nodes[i].GroupIDs, id)
			}
		} else {
			s.data.Nodes[i].GroupIDs = without(s.data.Nodes[i].GroupIDs, id)
		}
	}
	return s.saveLocked()
}

func (s *Store) DeleteGroup(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Groups {
		if s.data.Groups[i].ID == id {
			s.data.Groups = append(s.data.Groups[:i], s.data.Groups[i+1:]...)
			for j := range s.data.Nodes {
				s.data.Nodes[j].GroupIDs = without(s.data.Nodes[j].GroupIDs, id)
			}
			return s.saveLocked()
		}
	}
	return os.ErrNotExist
}

func (s *Store) Report(agentToken, ip, version string, metrics Metrics) (bool, error) {
	commands, err := s.ReportCommands(agentToken, ip, version, metrics, false)
	return commands.Upgrade, err
}

type NodeCommands struct {
	Upgrade bool   `json:"upgrade"`
	Power   string `json:"power,omitempty"`
}

var ErrNodeOffline = errors.New("node offline")
var ErrPowerPending = errors.New("power command already pending")
var ErrInvalidPowerAction = errors.New("invalid power action")

func (s *Store) ReportCommands(agentToken, ip, version string, metrics Metrics, powerSupported bool) (NodeCommands, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, removed := range s.data.RemovedNodes {
		if agentToken != "" && removed.AgentToken == agentToken {
			return NodeCommands{}, ErrRemovalPending
		}
	}
	for i := range s.data.Nodes {
		n := &s.data.Nodes[i]
		if agentToken != "" && n.AgentToken == agentToken {
			n.applyReport(ip, version, metrics, s.now())
			commands := n.takeCommands(powerSupported, s.now())
			return commands, s.saveLocked()
		}
	}
	return NodeCommands{}, os.ErrNotExist
}

// AutoReport updates an automatically enrolled node, creating it on the first
// heartbeat. Its display name defaults to the observed IP and dashboard edits win.
func (s *Store) AutoReport(nodeID, name, ip, version string, metrics Metrics) (bool, error) {
	commands, err := s.AutoReportCommands(nodeID, name, ip, version, metrics, false)
	return commands.Upgrade, err
}

func (s *Store) AutoReportCommands(nodeID, name, ip, version string, metrics Metrics, powerSupported bool) (NodeCommands, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, removed := range s.data.RemovedNodes {
		if removed.ID == nodeID {
			return NodeCommands{}, ErrRemovalPending
		}
	}
	for i := range s.data.Nodes {
		n := &s.data.Nodes[i]
		if n.ID == nodeID {
			if n.Name == "" {
				n.Name = ip
			}
			n.applyReport(ip, version, metrics, s.now())
			commands := n.takeCommands(powerSupported, s.now())
			return commands, s.saveLocked()
		}
	}
	n := Node{ID: nodeID, Name: ip, GroupIDs: []string{}, Sort: 0}
	n.applyReport(ip, version, metrics, s.now())
	s.data.Nodes = append(s.data.Nodes, n)
	return NodeCommands{}, s.saveLocked()
}

func (n *Node) takeCommands(powerSupported bool, now time.Time) NodeCommands {
	if n.PowerRequested != "" && now.Sub(n.PowerRequestedAt) > 2*time.Minute {
		n.PowerRequested, n.PowerRequestedAt = "", time.Time{}
	}
	if n.PowerRequested != "" {
		if powerSupported {
			command := NodeCommands{Power: n.PowerRequested}
			n.PowerRequested, n.PowerRequestedAt = "", time.Time{}
			return command
		}
		return NodeCommands{Upgrade: true}
	}
	upgrade := n.UpgradeRequested
	n.UpgradeRequested = false
	return NodeCommands{Upgrade: upgrade}
}

var trafficLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

// ResetNodeTraffic clears panel statistics without changing the agent's raw
// counters or online status. The next report establishes a fresh baseline,
// including for offline nodes, so pre-reset traffic cannot be counted again.
func (s *Store) ResetNodeTraffic(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Nodes {
		n := &s.data.Nodes[i]
		if n.ID != id {
			continue
		}
		previous := *n
		n.TrafficResetAt = s.now().UTC()
		n.TrafficResetPending = true
		n.TrafficDate = n.TrafficResetAt.In(trafficLocation).Format("2006-01-02")
		n.TodayUpload, n.TodayDownload = 0, 0
		n.YesterdayUpload, n.YesterdayDownload = 0, 0
		n.DayBeforeYesterdayUpload, n.DayBeforeYesterdayDownload = 0, 0
		n.UploadSinceReset, n.DownloadSinceReset = 0, 0
		if err := s.saveLocked(); err != nil {
			*n = previous
			return err
		}
		return nil
	}
	return os.ErrNotExist
}

// rollTrafficDay also runs on snapshot copies so offline nodes roll over.
func (n *Node) rollTrafficDay(now time.Time) {
	local := now.In(trafficLocation)
	date := local.Format("2006-01-02")
	if n.TrafficDate == date {
		return
	}
	previousTodayUpload, previousTodayDownload := n.TodayUpload, n.TodayDownload
	previousYesterdayUpload, previousYesterdayDownload := n.YesterdayUpload, n.YesterdayDownload
	n.DayBeforeYesterdayUpload, n.DayBeforeYesterdayDownload = 0, 0
	n.YesterdayUpload, n.YesterdayDownload = 0, 0
	if n.TrafficDate == local.AddDate(0, 0, -1).Format("2006-01-02") {
		n.DayBeforeYesterdayUpload, n.DayBeforeYesterdayDownload = previousYesterdayUpload, previousYesterdayDownload
		n.YesterdayUpload, n.YesterdayDownload = previousTodayUpload, previousTodayDownload
	} else if n.TrafficDate == local.AddDate(0, 0, -2).Format("2006-01-02") {
		n.DayBeforeYesterdayUpload, n.DayBeforeYesterdayDownload = previousTodayUpload, previousTodayDownload
	}
	n.TodayUpload, n.TodayDownload = 0, 0
	n.TrafficDate = date
}

// Counter high-water marks prevent temporary drops or failed samples from
// counting the old lifetime total a second time when counters recover.
func (n *Node) applyReport(ip, version string, metrics Metrics, now time.Time) {
	initialized := n.TrafficDate != "" && !n.LastSeen.IsZero()
	n.rollTrafficDay(now)
	if !n.BaselineReady {
		n.UploadBaseline, n.DownloadBaseline = n.TotalUpload, n.TotalDownload
	}
	reset := n.TrafficResetPending || !initialized ||
		(metrics.BootID != "" && metrics.BootID != n.BootID) ||
		(metrics.NetworkSet != "" && metrics.NetworkSet != n.NetworkSet)
	// Older agents have no boot ID. Only an actual decrease in uptime is used,
	// never the difference between network arrival time and uptime.
	if metrics.BootID == "" && metrics.Uptime > 0 && metrics.Uptime < n.Uptime {
		reset = true
	}
	if reset {
		n.UploadBaseline, n.DownloadBaseline = metrics.TotalUpload, metrics.TotalDownload
	} else if now.After(n.LastSeen) {
		delta := func(current uint64, baseline *uint64) uint64 {
			if current <= *baseline {
				return 0
			}
			d := current - *baseline
			*baseline = current
			return d
		}
		up, down := delta(metrics.TotalUpload, &n.UploadBaseline), delta(metrics.TotalDownload, &n.DownloadBaseline)
		if !n.TrafficResetAt.IsZero() {
			n.UploadSinceReset += up
			n.DownloadSinceReset += down
		}
		local := now.In(trafficLocation)
		midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, trafficLocation)
		yesterday := midnight.AddDate(0, 0, -1)
		dayBeforeYesterday := midnight.AddDate(0, 0, -2)
		portion := func(value uint64, from, to time.Time) uint64 {
			if from.Before(n.LastSeen) {
				from = n.LastSeen
			}
			if to.After(now) {
				to = now
			}
			if !to.After(from) {
				return 0
			}
			if from.Equal(n.LastSeen) && to.Equal(now) {
				return value
			}
			return uint64(float64(value) * (float64(to.Sub(from)) / float64(now.Sub(n.LastSeen))))
		}
		n.TodayUpload += portion(up, midnight, now)
		n.TodayDownload += portion(down, midnight, now)
		n.YesterdayUpload += portion(up, yesterday, midnight)
		n.YesterdayDownload += portion(down, yesterday, midnight)
		n.DayBeforeYesterdayUpload += portion(up, dayBeforeYesterday, yesterday)
		n.DayBeforeYesterdayDownload += portion(down, dayBeforeYesterday, yesterday)
	}
	n.BaselineReady = true
	n.TrafficResetPending = false
	// A failed uptime read must not destroy a valid legacy reboot baseline.
	if metrics.Uptime == 0 && !reset {
		metrics.Uptime = n.Uptime
	}
	n.IP, n.Version, n.LastSeen, n.Metrics = ip, version, now.UTC(), metrics
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func without(values []string, target string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != target {
			result = append(result, value)
		}
	}
	return result
}

func unique(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func (s *Store) RequestUpgrade(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Nodes {
		if s.data.Nodes[i].ID == id {
			s.data.Nodes[i].UpgradeRequested = true
			return s.saveLocked()
		}
	}
	return os.ErrNotExist
}

func (s *Store) RequestUpgradeAll() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Nodes {
		s.data.Nodes[i].UpgradeRequested = true
	}
	if len(s.data.Nodes) == 0 {
		return 0, nil
	}
	return len(s.data.Nodes), s.saveLocked()
}

func (s *Store) RequestPower(id, action string) error {
	if action != "reboot" && action != "shutdown" {
		return ErrInvalidPowerAction
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for i := range s.data.Nodes {
		n := &s.data.Nodes[i]
		if n.ID != id {
			continue
		}
		if n.LastSeen.IsZero() || now.Sub(n.LastSeen) > 15*time.Second {
			return ErrNodeOffline
		}
		if n.PowerRequested != "" && now.Sub(n.PowerRequestedAt) <= 2*time.Minute {
			return ErrPowerPending
		}
		n.PowerRequested, n.PowerRequestedAt = action, now
		return s.saveLocked()
	}
	return os.ErrNotExist
}
