// Package status writes the daemon's current view to a JSON file that the LuCI
// rpcd backend reads. Keeping status in a file (rather than registering a ubus
// object from Go) keeps the daemon a dependency-free static binary.
package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Dir is the runtime directory for status output.
const Dir = "/var/run/simplewan"

// File is the status document path.
const File = Dir + "/status.json"

// Iface is the per-WAN status.
type Iface struct {
	Name       string `json:"name"`
	IfName     string `json:"ifname"`
	Device     string `json:"device"`
	Primary    bool   `json:"primary"`
	Online     bool   `json:"online"`
	Selected   bool   `json:"selected"`
	HasRoute   bool   `json:"has_route"`
	Metric     int    `json:"metric"`
	LossPct    int    `json:"loss_pct"`
	RTTms      int64  `json:"rtt_ms"`
	LastChange int64  `json:"last_change"` // unix seconds
}

// Doc is the full status document.
type Doc struct {
	Updated      int64   `json:"updated"`
	Enabled      bool    `json:"enabled"`
	PingTarget   string  `json:"ping_target"`
	Selected     string  `json:"selected"`
	LastSwitch   int64   `json:"last_switch"`
	LastSwitchTo string  `json:"last_switch_to"`
	Ifaces       []Iface `json:"ifaces"`
}

// Write atomically replaces the status file.
func Write(d *Doc) error {
	d.Updated = time.Now().Unix()
	if err := os.MkdirAll(Dir, 0o755); err != nil {
		return err
	}
	buf, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(Dir, ".status.json.tmp")
	if err := os.WriteFile(tmp, buf, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, File)
}
