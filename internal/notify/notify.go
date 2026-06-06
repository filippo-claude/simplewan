// Package notify sends failover/recovery emails through the Postmark API.
//
// Sending is always best-effort and time-bounded: a slow or failing Postmark
// call must never stall a routing decision, so callers invoke Send from a
// goroutine and ignore the (logged) error.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const endpoint = "https://api.postmarkapp.com/email"

// Config mirrors the UCI [notify] section.
type Config struct {
	Enabled       bool
	Token         string
	From          string
	To            string
	SubjectPrefix string
}

func (c Config) ready() bool {
	return c.Enabled && c.Token != "" && c.From != "" && c.To != ""
}

type payload struct {
	From          string `json:"From"`
	To            string `json:"To"`
	Subject       string `json:"Subject"`
	TextBody      string `json:"TextBody"`
	MessageStream string `json:"MessageStream"`
}

// Send posts one email. It returns nil (without contacting Postmark) when
// notifications are disabled or not fully configured.
func (c Config) Send(ctx context.Context, subject, body string) error {
	if !c.ready() {
		return nil
	}
	if c.SubjectPrefix != "" {
		subject = strings.TrimSpace(c.SubjectPrefix) + " " + subject
	}
	buf, err := json.Marshal(payload{
		From:          c.From,
		To:            c.To,
		Subject:       subject,
		TextBody:      body,
		MessageStream: "outbound",
	})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Postmark-Server-Token", c.Token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("postmark returned %s", resp.Status)
	}
	return nil
}
