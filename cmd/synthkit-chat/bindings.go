// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/rknightion/synthkit/internal/chatsim/script"
)

// Bindings ties a scenario's logical names to one Slack workspace. It holds identifiers and the
// NAMES of the environment variables that carry secrets, never a secret itself.
type Bindings struct {
	Channels       map[string]string `yaml:"channels"`
	OperatorUserID string            `yaml:"operator_user_id"`
	IRM            struct {
		BotIDs []string `yaml:"bot_ids"`
		AppIDs []string `yaml:"app_ids"`
		// GrafanaURL and TokenEnv are the stack a seed with wipe.irm resets: TokenEnv names a
		// service account token with IRM admin rights.
		GrafanaURL string `yaml:"grafana_url"`
		TokenEnv   string `yaml:"token_env"`
	} `yaml:"irm"`
	Control struct {
		URL          string          `yaml:"url"`
		TokenEnv     string          `yaml:"token_env"`
		PollInterval script.Duration `yaml:"poll_interval"`
	} `yaml:"control"`
	Slack struct {
		BotTokenEnv string `yaml:"bot_token_env"`
		AppTokenEnv string `yaml:"app_token_env"`
		// UserTokenEnv names the workspace owner's user token (xoxp-) a reset uses to delete
		// everyone's messages, archive channels and post the operator's seeded lines.
		UserTokenEnv string `yaml:"user_token_env"`
	} `yaml:"slack"`
	OpenRouter struct {
		APIKeyEnv     string   `yaml:"api_key_env"`
		ProviderOrder []string `yaml:"provider_order"`
		ZDR           bool     `yaml:"zdr"`
	} `yaml:"openrouter"`
	// ControlDM is how the app speaks in the operator's control DM (needs
	// chat:write.customize); empty means the app's own name and icon.
	ControlDM struct {
		Name  string `yaml:"name"`
		Emoji string `yaml:"emoji"`
	} `yaml:"control_dm"`
	JournalDir string `yaml:"journal_dir"`
}

// Default secret variable names.
const (
	defaultBotTokenEnv  = "SLACK_BOT_TOKEN"
	defaultAppTokenEnv  = "SLACK_APP_TOKEN"
	defaultUserTokenEnv = "SLACK_USER_TOKEN"
	defaultAPIKeyEnv    = "OPENROUTER_API_KEY"
	defaultControlEnv   = "CONTROL_TOKEN"
	defaultIRMTokenEnv  = "GRAFANA_IRM_TOKEN"
	defaultJournalDir   = "chatsim-runs"
)

var (
	slackChannelRe = regexp.MustCompile(`^[CGD][A-Z0-9]{6,}$`)
	slackUserRe    = regexp.MustCompile(`^[UW][A-Z0-9]{6,}$`)
	slackBotRe     = regexp.MustCompile(`^B[A-Z0-9]{6,}$`)
	slackAppRe     = regexp.MustCompile(`^A[A-Z0-9]{6,}$`)
	envNameRe      = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
)

func loadBindings(path string) (*Bindings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var b Bindings
	if err := dec.Decode(&b); err != nil {
		return nil, fmt.Errorf("%s: decode: %w", path, err)
	}
	b.defaults()
	if err := b.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &b, nil
}

func (b *Bindings) defaults() {
	set := func(p *string, v string) {
		if *p == "" {
			*p = v
		}
	}
	set(&b.Slack.BotTokenEnv, defaultBotTokenEnv)
	set(&b.Slack.AppTokenEnv, defaultAppTokenEnv)
	set(&b.Slack.UserTokenEnv, defaultUserTokenEnv)
	set(&b.OpenRouter.APIKeyEnv, defaultAPIKeyEnv)
	set(&b.Control.TokenEnv, defaultControlEnv)
	set(&b.IRM.TokenEnv, defaultIRMTokenEnv)
	set(&b.JournalDir, defaultJournalDir)
	if b.Control.PollInterval.Duration == 0 {
		b.Control.PollInterval.Duration = time.Second
	}
}

func (b *Bindings) validate() error {
	var errs []string
	for l, id := range b.Channels {
		if !slackChannelRe.MatchString(id) {
			errs = append(errs, fmt.Sprintf("channels.%s: %q is not a Slack channel id", l, id))
		}
	}
	if b.OperatorUserID != "" && !slackUserRe.MatchString(b.OperatorUserID) {
		errs = append(errs, fmt.Sprintf("operator_user_id %q is not a Slack user id", b.OperatorUserID))
	}
	for _, id := range b.IRM.BotIDs {
		if !slackBotRe.MatchString(id) {
			errs = append(errs, fmt.Sprintf("irm.bot_ids: %q is not a Slack bot id", id))
		}
	}
	for _, id := range b.IRM.AppIDs {
		if !slackAppRe.MatchString(id) {
			errs = append(errs, fmt.Sprintf("irm.app_ids: %q is not a Slack app id", id))
		}
	}
	for _, n := range []string{b.Slack.BotTokenEnv, b.Slack.AppTokenEnv, b.Slack.UserTokenEnv, b.IRM.TokenEnv, b.OpenRouter.APIKeyEnv, b.Control.TokenEnv} {
		if !envNameRe.MatchString(n) {
			errs = append(errs, fmt.Sprintf("%q is not an environment variable name (bindings hold names, never secrets)", n))
		}
	}
	if u := b.IRM.GrafanaURL; u != "" && !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
		errs = append(errs, "irm.grafana_url must be http:// or https://")
	}
	if b.Control.URL != "" && !strings.HasPrefix(b.Control.URL, "http://") && !strings.HasPrefix(b.Control.URL, "https://") {
		errs = append(errs, "control.url must be http:// or https://")
	}
	if len(errs) > 0 {
		return errors.New("invalid bindings:\n  - " + strings.Join(errs, "\n  - "))
	}
	return nil
}

// forScenario checks the bindings cover the scenario and the live-run essentials.
func (b *Bindings) forScenario(sc *script.Scenario) error {
	var errs []string
	for _, id := range sc.ChannelIDs() {
		if sc.Channels[id].Bind == nil && b.Channels[id] == "" {
			errs = append(errs, fmt.Sprintf("channels.%s is not bound", id))
		}
	}
	if b.OperatorUserID == "" {
		errs = append(errs, "operator_user_id is required (the kill switch accepts only this user)")
	}
	if len(b.IRM.BotIDs)+len(b.IRM.AppIDs) == 0 {
		errs = append(errs, "irm.bot_ids or irm.app_ids is required to recognise IRM posts (capture them with tap)")
	}
	if len(errs) > 0 {
		return errors.New("bindings do not cover the scenario:\n  - " + strings.Join(errs, "\n  - "))
	}
	return nil
}

// secret reads the variable named name.
func secret(name string) (string, error) {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return "", fmt.Errorf("environment variable %s is not set", name)
	}
	return v, nil
}
