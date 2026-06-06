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

// Notify holds the Postmark notification settings.
type Notify struct {
	Enabled       bool
	Token         string // Postmark Server API token
	From          string // verified Postmark sender address
	To            string
	SubjectPrefix string
}

// Config is the parsed configuration.
//
// Failover behaviour (probe cadence, dead-link and loss thresholds) is fixed in
// the daemon; recovery_time is the only numeric knob. The primary/backup base
// route metrics are read from netifd, not configured here.
type Config struct {
	Enabled        bool
	Primary        string // netifd interface preferred while healthy
	Backup         string // netifd interface to fail over to
	PingTarget     string // single IPv4 target
	RecoveryTime   int    // seconds the primary must stay healthy before switching back to it
	FlushConntrack bool
	Notify         Notify
}

func def() *Config {
	return &Config{
		Enabled:        true,
		PingTarget:     "1.1.1.1",
		RecoveryTime:   300,
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
			if v := s.opts["primary"]; v != "" {
				c.Primary = v
			}
			if v := s.opts["backup"]; v != "" {
				c.Backup = v
			}
			if v := s.opts["ping_target"]; v != "" {
				c.PingTarget = v
			}
			c.RecoveryTime = intOpt(s.opts["recovery_time"], c.RecoveryTime)
			c.FlushConntrack = boolOpt(s.opts["flush_conntrack"], c.FlushConntrack)
		case "notify":
			c.Notify.Enabled = boolOpt(s.opts["enabled"], false)
			c.Notify.Token = s.opts["postmark_token"]
			c.Notify.From = s.opts["mail_from"]
			c.Notify.To = s.opts["mail_to"]
			c.Notify.SubjectPrefix = s.opts["subject_prefix"]
		}
	}
	if c.Primary == "" || c.Backup == "" {
		return nil, fmt.Errorf("both 'primary' and 'backup' interfaces must be set")
	}
	if c.Primary == c.Backup {
		return nil, fmt.Errorf("'primary' and 'backup' must differ (both %q)", c.Primary)
	}
	return c, nil
}
