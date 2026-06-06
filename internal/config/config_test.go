package config

import (
	"bufio"
	"strings"
	"testing"
)

const sample = `
config globals 'globals'
	option enabled '1'
	option ping_target '9.9.9.9'
	option interval '7'
	option recovery_time '120'

config interface 'primary'
	option ifname 'wan'
	option device 'eth1'
	option metric '10'
	option priority '1'

config interface 'backup'
	option ifname 'wan2'
	option metric '20'
	option priority '2'

config notify
	option enabled '1'
	option postmark_token 'abc 123'
	option mail_from 'r@example.com'
	option mail_to 'me@example.com'
`

func TestParse(t *testing.T) {
	secs, err := parse(bufio.NewScanner(strings.NewReader(sample)))
	if err != nil {
		t.Fatal(err)
	}
	c, err := fromSections(secs)
	if err != nil {
		t.Fatal(err)
	}
	if c.PingTarget != "9.9.9.9" || c.Interval != 7 || c.RecoveryTime != 120 {
		t.Errorf("globals not parsed: %+v", c)
	}
	if c.Timeout != 2 { // default preserved
		t.Errorf("default timeout = %d, want 2", c.Timeout)
	}
	if len(c.Ifaces) != 2 {
		t.Fatalf("ifaces = %d, want 2", len(c.Ifaces))
	}
	if c.Ifaces[0].Device != "eth1" || c.Ifaces[0].Metric != 10 || c.Ifaces[0].Priority != 1 {
		t.Errorf("primary parsed wrong: %+v", c.Ifaces[0])
	}
	if !c.Notify.Enabled || c.Notify.Token != "abc 123" {
		t.Errorf("notify parsed wrong: %+v", c.Notify)
	}
}

func TestNeedsTwoInterfaces(t *testing.T) {
	secs, _ := parse(bufio.NewScanner(strings.NewReader(`
config interface 'only'
	option ifname 'wan'
`)))
	if _, err := fromSections(secs); err == nil {
		t.Fatal("expected error for a single interface")
	}
}
