package notify

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestEmailCCAppearsInHeaderAndEnvelopeWithoutDuplicates(t *testing.T) {
	f := newFakeSMTP(t)
	cfg := emailCfg(t, f, map[string]any{"to": []any{"head@example.invalid"}, "cc": []string{"HEAD@example.invalid", "dev@example.invalid", "DEV@example.invalid", "other@example.invalid"}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := (emailChannel{}).Send(ctx, cfg, singleMsg()); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	commands := append([]string{}, f.commands...)
	f.mu.Unlock()
	recipients := []string{}
	for _, command := range commands {
		if strings.HasPrefix(command, "RCPT TO:") {
			recipients = append(recipients, command)
		}
	}
	want := []string{"RCPT TO:<head@example.invalid>", "RCPT TO:<dev@example.invalid>", "RCPT TO:<other@example.invalid>"}
	if !reflect.DeepEqual(recipients, want) {
		t.Fatalf("SMTP recipients=%v want=%v", recipients, want)
	}
	body := f.body()
	if !strings.Contains(body, "To: head@example.invalid\r\n") || !strings.Contains(body, "Cc: dev@example.invalid, other@example.invalid\r\n") {
		t.Fatalf("incorrect To/Cc headers: %s", body)
	}
	if !reflect.DeepEqual(cfg["cc"], []string{"HEAD@example.invalid", "dev@example.invalid", "DEV@example.invalid", "other@example.invalid"}) {
		t.Fatal("sender mutated shared channel configuration")
	}
}

func TestEmailCCRejectsMalformedAddressBeforeSending(t *testing.T) {
	for _, address := range []string{"invalid", "person@example.invalid\r\nBcc: other@example.invalid"} {
		cfg := map[string]any{"host": "smtp.example.invalid", "port": 25, "from": "from@example.invalid", "to": []any{"to@example.invalid"}, "cc": []any{address}}
		if err := (emailChannel{}).Validate(cfg); err == nil {
			t.Fatalf("accepted malformed CC %q", address)
		}
	}
}
