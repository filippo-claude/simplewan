// Package config loads simplewan's UCI configuration from /etc/config/simplewan.
//
// It implements a small parser for the subset of the UCI format the package
// uses (named "config" sections with "option" values); this keeps the daemon
// dependency-free and testable off-target, rather than shelling out to uci(1).
package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// DefaultPath is the on-router location of the configuration file.
const DefaultPath = "/etc/config/simplewan"

// Iface describes one upstream WAN.
type Iface struct {
	Name     string // UCI section name
	IfName   string // netifd logical interface (e.g. "wan")
	Device   string // L3 device override (e.g. "eth1"); resolved from IfName if empty
	Metric   int    // preferred default-route metric when this WAN is selected
	Priority int    // lower is more preferred; ties broken by config order
}

// Notify holds the Postmark notification settings.
type Notify struct {
	Enabled       bool
	Token         string // Postmark Server API token
	From          string // verified Postmark sender address
	To            string
	SubjectPrefix string
}

// Config is the parsed configuration.
type Config struct {
	Enabled        bool
	PingTarget     string // single IPv4 target
	Interval       int    // seconds between checks
	Timeout        int    // per-ping timeout, seconds
	Count          int    // pings sent per check
	Down           int    // consecutive failed checks to declare a WAN offline
	Up             int    // consecutive good checks to declare a WAN online
	RecoveryTime   int    // seconds a preferred WAN must stay healthy before switching back to it
	DemoteOffset   int    // metric added to demote a WAN below the selected one
	FlushConntrack bool
	Ifaces         []Iface
	Notify         Notify
}

func def() *Config {
	return &Config{
		Enabled:        true,
		PingTarget:     "1.1.1.1",
		Interval:       5,
		Timeout:        2,
		Count:          1,
		Down:           3,
		Up:             3,
		RecoveryTime:   300,
		DemoteOffset:   1000,
		FlushConntrack: true,
	}
}

type section struct {
	typ  string
	name string
	opts map[string]string
}

func tokenize(line string) []string {
	// Splits a UCI line into tokens, honoring single/double quotes.
	var toks []string
	var b strings.Builder
	var quote rune
	inTok := false
	flush := func() {
		if inTok {
			toks = append(toks, b.String())
			b.Reset()
			inTok = false
		}
	}
	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			inTok = true
		case r == ' ' || r == '\t':
			flush()
		default:
			inTok = true
			b.WriteRune(r)
		}
	}
	flush()
	return toks
}

func parse(r *bufio.Scanner) ([]section, error) {
	var secs []section
	var cur *section
	for r.Scan() {
		line := strings.TrimSpace(r.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		toks := tokenize(line)
		if len(toks) == 0 {
			continue
		}
		switch toks[0] {
		case "config":
			if len(toks) < 2 {
				return nil, fmt.Errorf("config line without type: %q", line)
			}
			secs = append(secs, section{typ: toks[1], opts: map[string]string{}})
			cur = &secs[len(secs)-1]
			if len(toks) >= 3 {
				cur.name = toks[2]
			}
		case "option":
			if cur == nil || len(toks) < 3 {
				continue
			}
			cur.opts[toks[1]] = toks[2]
		case "list":
			// not used by this package's schema; ignored on purpose
		}
	}
	return secs, r.Err()
}

func boolOpt(s string, dflt bool) bool {
	switch strings.ToLower(s) {
	case "1", "true", "yes", "on", "enabled":
		return true
	case "0", "false", "no", "off", "disabled":
		return false
	default:
		return dflt
	}
}

func intOpt(s string, dflt int) int {
	if s == "" {
		return dflt
	}
	if v, err := strconv.Atoi(s); err == nil {
		return v
	}
	return dflt
}

// Load reads and parses the configuration at path.
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	secs, err := parse(bufio.NewScanner(f))
	if err != nil {
		return nil, err
	}
	return fromSections(secs)
}

func fromSections(secs []section) (*Config, error) {
	c := def()
	for i := range secs {
		s := &secs[i]
		switch s.typ {
		case "globals":
			c.Enabled = boolOpt(s.opts["enabled"], c.Enabled)
			if v := s.opts["ping_target"]; v != "" {
				c.PingTarget = v
			}
			c.Interval = intOpt(s.opts["interval"], c.Interval)
			c.Timeout = intOpt(s.opts["timeout"], c.Timeout)
			c.Count = intOpt(s.opts["count"], c.Count)
			c.Down = intOpt(s.opts["down"], c.Down)
			c.Up = intOpt(s.opts["up"], c.Up)
			c.RecoveryTime = intOpt(s.opts["recovery_time"], c.RecoveryTime)
			c.DemoteOffset = intOpt(s.opts["demote_offset"], c.DemoteOffset)
			c.FlushConntrack = boolOpt(s.opts["flush_conntrack"], c.FlushConntrack)
		case "interface":
			it := Iface{
				Name:     s.name,
				IfName:   s.opts["ifname"],
				Device:   s.opts["device"],
				Metric:   intOpt(s.opts["metric"], 0),
				Priority: intOpt(s.opts["priority"], len(c.Ifaces)+1),
			}
			if it.IfName == "" {
				it.IfName = s.name
			}
			c.Ifaces = append(c.Ifaces, it)
		case "notify":
			c.Notify.Enabled = boolOpt(s.opts["enabled"], false)
			c.Notify.Token = s.opts["postmark_token"]
			c.Notify.From = s.opts["mail_from"]
			c.Notify.To = s.opts["mail_to"]
			c.Notify.SubjectPrefix = s.opts["subject_prefix"]
		}
	}
	if len(c.Ifaces) < 2 {
		return nil, fmt.Errorf("need at least two interface sections, found %d", len(c.Ifaces))
	}
	return c, nil
}
