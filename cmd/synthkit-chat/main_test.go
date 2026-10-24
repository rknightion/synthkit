// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var (
	example  = filepath.Join("..", "..", "examples", "chat", "backend-latency.chat.yaml")
	bindings = filepath.Join("..", "..", "examples", "chat", "bindings.example.yaml")
)

func runCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out, errb strings.Builder
	err := run(context.Background(), args, strings.NewReader(""), &out, &errb)
	return out.String() + errb.String(), err
}

func TestValidateExampleWithBindings(t *testing.T) {
	out, err := runCmd(t, "validate", "-scenario", example, "-bindings", bindings)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ok: content-gen-latency-drill") {
		t.Fatalf("out = %q", out)
	}
}

func TestBindingsRejectSecretsAndUnknownFields(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		p := filepath.Join(dir, "b.yaml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if _, err := loadBindings(write("channels: { ops: C0000000001 }\nslack: { bot_token_env: \"xoxb-x\" }\n")); err == nil ||
		!strings.Contains(err.Error(), "not an environment variable name") {
		t.Fatalf("a token in place of a variable name was accepted: %v", err)
	}
	if _, err := loadBindings(write("channels: { ops: C0000000001 }\nsurprise: 1\n")); err == nil {
		t.Fatal("unknown field accepted")
	}
	b, err := loadBindings(write("channels: { ops: C0000000001 }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if b.Slack.AppTokenEnv != defaultAppTokenEnv || b.Control.PollInterval.Seconds() != 1 {
		t.Fatalf("defaults not applied: %+v", b)
	}
	if _, err := runCmd(t, "validate", "-scenario", example, "-bindings", write("channels: { ops: C0000000001 }\n")); err == nil ||
		!strings.Contains(err.Error(), "operator_user_id is required") {
		t.Fatalf("incomplete bindings passed validate: %v", err)
	}
}

func TestCommandErrors(t *testing.T) {
	if _, err := runCmd(t); err == nil {
		t.Fatal("no command accepted")
	}
	if _, err := runCmd(t, "bogus"); err == nil {
		t.Fatal("unknown command accepted")
	}
	if _, err := runCmd(t, "run", "-scenario", example); err == nil || !strings.Contains(err.Error(), "-bindings") {
		t.Fatalf("run without bindings: %v", err)
	}
	if _, err := runCmd(t, "console", "-scenario", example, "-llm", "nope"); err == nil {
		t.Fatal("unknown -llm accepted")
	}
}

// Console mode with the fake LLM ends cleanly when stdin closes, without any network.
func TestConsoleQuitsOnEOF(t *testing.T) {
	j := filepath.Join(t.TempDir(), "j.jsonl")
	var out strings.Builder
	err := run(context.Background(), []string{"console", "-scenario", example, "-no-color", "-journal", j},
		strings.NewReader("status\n:quit\n"), &out, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "waiting for trigger") || !strings.Contains(out.String(), "run ended") {
		t.Fatalf("console output = %q", out.String())
	}
}
