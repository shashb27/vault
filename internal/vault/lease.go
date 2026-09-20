package vault

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Leases are advisory: they ride the same sync as everything else, so they
// protect against forgetting, not racing. One lease per session.

type Lease struct {
	User       string `json:"user"`
	Host       string `json:"host"`
	PID        int    `json:"pid"`
	SessionID  string `json:"session_id,omitempty"`
	AcquiredAt string `json:"acquired_at"`
	TTLMinutes int    `json:"ttl_minutes"`
	Heartbeats int    `json:"heartbeats,omitempty"`
	Expired    bool   `json:"-"`
	AgeSecs    int64  `json:"-"`
}

func readLease(path string) *Lease {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var l Lease
	if json.Unmarshal(b, &l) != nil || l.AcquiredAt == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, l.AcquiredAt)
	if err != nil {
		return nil
	}
	ttl := l.TTLMinutes
	if ttl == 0 {
		ttl = leaseTTLMin
	}
	l.AgeSecs = int64(time.Since(t).Seconds())
	l.Expired = l.AgeSecs > int64(ttl)*60
	return &l
}

func (v *Vault) leasePath(sid string) string { return filepath.Join(v.Dir, "leases", sid+".json") }

// activeLeases returns fresh per-session leases plus "vault" for a legacy
// vault-wide lease left by a v0.1 client.
func (v *Vault) activeLeases() map[string]*Lease {
	out := map[string]*Lease{}
	matches, _ := filepath.Glob(filepath.Join(v.Dir, "leases", "*.json"))
	for _, p := range matches {
		if l := readLease(p); l != nil && !l.Expired {
			out[strings.TrimSuffix(filepath.Base(p), ".json")] = l
		}
	}
	if l := readLease(filepath.Join(v.Dir, "lease.json")); l != nil && !l.Expired {
		out["vault"] = l
	}
	return out
}

func (v *Vault) writeLease(sid string, beats int) error {
	os.MkdirAll(filepath.Join(v.Dir, "leases"), 0o755)
	l := Lease{User: v.self.User, Host: v.self.Host, PID: os.Getpid(), SessionID: sid,
		AcquiredAt: nowISO(), TTLMinutes: leaseTTLMin, Heartbeats: beats}
	b, _ := json.Marshal(l)
	tmp := filepath.Join(v.Dir, "leases", fmt.Sprintf(".%s.%d", sid, os.Getpid()))
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, v.leasePath(sid))
}

func (v *Vault) acquireLease(sid string, steal bool) error {
	if l := readLease(filepath.Join(v.Dir, "lease.json")); l != nil && !l.Expired &&
		(l.User != v.self.User || l.Host != v.self.Host) && !steal {
		return fmt.Errorf("%s@%s is working in this vault with an older vault version (since %s).\n  Ask them to finish and to run 'vault update'. If you are sure they are done:   re-run with --steal", l.User, l.Host, l.AcquiredAt)
	}
	if l := readLease(v.leasePath(sid)); l != nil {
		other := l.User != v.self.User || l.Host != v.self.Host
		if !other && l.PID != os.Getpid() && processAlive(l.PID) {
			other = true // another live vault instance of my own on this machine
		}
		if !l.Expired && other {
			if steal {
				warn("taking over the session from %s@%s (they opened it %s)", l.User, l.Host, l.AcquiredAt)
			} else {
				return fmt.Errorf("%s@%s is in this session right now (opened %s).\n  Two people in one session at once forks it and loses turns.\n  Ask them to exit, or if you are SURE they are done:   re-run with --steal", l.User, l.Host, l.AcquiredAt)
			}
		}
	}
	return v.writeLease(sid, 0)
}

func (v *Vault) releaseLease(sid string) {
	l := readLease(v.leasePath(sid))
	if l != nil && l.User == v.self.User && l.Host == v.self.Host && l.PID == os.Getpid() {
		os.Remove(v.leasePath(sid))
	}
}

// heartbeat refreshes our lease while Claude runs so long sessions never look free.
func (v *Vault) heartbeat(sid string, stop <-chan struct{}) {
	interval := time.Duration(heartbeatSecs()) * time.Second
	beats := 0
	for {
		select {
		case <-stop:
			return
		case <-time.After(interval):
		}
		l := readLease(v.leasePath(sid))
		if l == nil || l.PID != os.Getpid() {
			return
		}
		beats++
		v.writeLease(sid, beats)
	}
}

func heartbeatSecs() int {
	if s := os.Getenv("VAULT_LEASE_HEARTBEAT_SECS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
	}
	return 300
}

func nowISO() string { return time.Now().UTC().Format("2006-01-02T15:04:05Z") }

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return signalAlive(p)
}
