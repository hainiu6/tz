package store

import (
	"errors"
	"os"
	"time"
)

type DDNSTask struct {
	ID         string    `json:"id"`
	Domain     string    `json:"domain"`
	Type       string    `json:"type"`
	Target     string    `json:"target"`
	NextRun    time.Time `json:"nextRun"`
	Repeat     string    `json:"repeat"`
	RunDay     int       `json:"runDay,omitempty"`
	Enabled    bool      `json:"enabled"`
	Completed  bool      `json:"completed,omitempty"`
	LastRun    time.Time `json:"lastRun,omitempty"`
	LastStatus string    `json:"lastStatus,omitempty"`
}

type DDNSLog struct {
	At      time.Time `json:"at"`
	Trigger string    `json:"trigger"`
	Status  string    `json:"status"`
	From    string    `json:"from,omitempty"`
	To      string    `json:"to"`
	Message string    `json:"message,omitempty"`
}

func (s *Store) DDNSCredentials() (string, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.DDNSAccessKeyID, s.data.DDNSSecretAccessKey
}

func (s *Store) SetDDNSCredentials(accessKeyID, secretAccessKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.DDNSAccessKeyID = accessKeyID
	s.data.DDNSSecretAccessKey = secretAccessKey
	return s.saveLocked()
}

func (s *Store) DDNSTasks() []DDNSTask {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]DDNSTask{}, s.data.DDNSTasks...)
}

func (s *Store) DDNSTask(id string) (DDNSTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, task := range s.data.DDNSTasks {
		if task.ID == id {
			return task, nil
		}
	}
	return DDNSTask{}, os.ErrNotExist
}

func (s *Store) CreateDDNSTask(task DDNSTask) (DDNSTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task.ID = token(8)
	task.Enabled = true
	s.data.DDNSTasks = append(s.data.DDNSTasks, task)
	return task, s.saveLocked()
}

func (s *Store) UpdateDDNSTask(id string, replacement DDNSTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.DDNSTasks {
		if s.data.DDNSTasks[i].ID == id {
			replacement.ID = id
			replacement.Enabled = s.data.DDNSTasks[i].Enabled
			replacement.LastRun = s.data.DDNSTasks[i].LastRun
			replacement.LastStatus = s.data.DDNSTasks[i].LastStatus
			s.data.DDNSTasks[i] = replacement
			return s.saveLocked()
		}
	}
	return os.ErrNotExist
}

func (s *Store) SetDDNSTaskEnabled(id string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.DDNSTasks {
		if s.data.DDNSTasks[i].ID == id {
			s.data.DDNSTasks[i].Enabled = enabled
			if enabled {
				s.data.DDNSTasks[i].Completed = false
			}
			return s.saveLocked()
		}
	}
	return os.ErrNotExist
}

func (s *Store) DeleteDDNSTask(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.DDNSTasks {
		if s.data.DDNSTasks[i].ID == id {
			s.data.DDNSTasks = append(s.data.DDNSTasks[:i], s.data.DDNSTasks[i+1:]...)
			delete(s.data.DDNSLogs, id)
			return s.saveLocked()
		}
	}
	return os.ErrNotExist
}

func (s *Store) DueDDNSTasks(now time.Time) []DDNSTask {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var due []DDNSTask
	for _, task := range s.data.DDNSTasks {
		if task.Enabled && !task.NextRun.After(now) {
			due = append(due, task)
		}
	}
	return due
}

// BeginDDNSRun advances a scheduled task before the network call. This prevents
// concurrent scheduler ticks from executing the same occurrence twice.
func (s *Store) BeginDDNSRun(id string, scheduled bool, now time.Time) (DDNSTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.DDNSTasks {
		task := &s.data.DDNSTasks[i]
		if task.ID != id {
			continue
		}
		if scheduled {
			if !task.Enabled || task.NextRun.After(now) {
				return DDNSTask{}, errors.New("task not due")
			}
			if task.Repeat == "once" {
				task.Enabled = false
				task.Completed = true
			} else {
				for !task.NextRun.After(now) {
					task.NextRun = nextDDNSRun(task.NextRun, task.Repeat, task.RunDay)
				}
			}
			if err := s.saveLocked(); err != nil {
				return DDNSTask{}, err
			}
		}
		return *task, nil
	}
	return DDNSTask{}, os.ErrNotExist
}

func nextDDNSRun(current time.Time, repeat string, runDay int) time.Time {
	switch repeat {
	case "daily":
		return current.Add(24 * time.Hour)
	case "weekly":
		return current.Add(7 * 24 * time.Hour)
	case "monthly":
		local := current.In(time.FixedZone("CST", 8*3600))
		first := time.Date(local.Year(), local.Month()+1, 1, local.Hour(), local.Minute(), local.Second(), local.Nanosecond(), local.Location())
		lastDay := first.AddDate(0, 1, -1).Day()
		day := runDay
		if day < 1 || day > 31 {
			day = local.Day()
		}
		if day > lastDay {
			day = lastDay
		}
		return first.AddDate(0, 0, day-1).UTC()
	default:
		return current.Add(24 * time.Hour)
	}
}

func (s *Store) CompleteDDNSRun(id string, entry DDNSLog) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.DDNSTasks {
		if s.data.DDNSTasks[i].ID == id {
			s.data.DDNSTasks[i].LastRun = entry.At
			s.data.DDNSTasks[i].LastStatus = entry.Status
			if s.data.DDNSLogs == nil {
				s.data.DDNSLogs = make(map[string][]DDNSLog)
			}
			logs := append(s.data.DDNSLogs[id], entry)
			if len(logs) > 200 {
				logs = logs[len(logs)-200:]
			}
			s.data.DDNSLogs[id] = logs
			return s.saveLocked()
		}
	}
	return os.ErrNotExist
}

func (s *Store) DDNSLogs(id string) ([]DDNSLog, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, task := range s.data.DDNSTasks {
		if task.ID == id {
			logs := append([]DDNSLog{}, s.data.DDNSLogs[id]...)
			for i, j := 0, len(logs)-1; i < j; i, j = i+1, j-1 {
				logs[i], logs[j] = logs[j], logs[i]
			}
			return logs, nil
		}
	}
	return nil, os.ErrNotExist
}
