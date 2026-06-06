package config

import (
	"bufio"
	"strings"
	"testing"
)

const sample = `
config globals 'globals'
	option enabled '1'
	option primary 'wan'
	option backup 'wan2'
	option ping_target '9.9.9.9'
	option recovery_time '120'
	option flush_conntrack '0'

config notify 'notify'
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
	if c.Primary != "wan" || c.Backup != "wan2" {
		t.Errorf("interfaces parsed wrong: %+v", c)
	}
	if c.PingTarget != "9.9.9.9" || c.RecoveryTime != 120 {
		t.Errorf("globals not parsed: %+v", c)
	}
	if c.FlushConntrack {
		t.Errorf("flush_conntrack should be false")
	}
	if !c.Notify.Enabled || c.Notify.Token != "abc 123" {
		t.Errorf("notify parsed wrong: %+v", c.Notify)
	}
}

func TestRequiresPrimaryAndBackup(t *testing.T) {
	secs, _ := parse(bufio.NewScanner(strings.NewReader(`
config globals 'globals'
	option primary 'wan'
`)))
	if _, err := fromSections(secs); err == nil {
		t.Fatal("expected error when backup is missing")
	}
}

func TestPrimaryBackupMustDiffer(t *testing.T) {
	secs, _ := parse(bufio.NewScanner(strings.NewReader(`
config globals 'globals'
	option primary 'wan'
	option backup 'wan'
`)))
	if _, err := fromSections(secs); err == nil {
		t.Fatal("expected error when primary == backup")
	}
}
