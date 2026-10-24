// SPDX-License-Identifier: AGPL-3.0-only

// Command synthkit-chat runs chat scenarios: a cast of simulated coworkers who talk in Slack
// around a SynthKit failure scenario and the real alert it raises. It is offline tooling, never
// linked into the emit binary.
//
//	synthkit-chat validate -scenario story.chat.yaml [-bindings bindings.yaml]
//	synthkit-chat console  -scenario story.chat.yaml [-llm fake|openrouter] [-trigger wait|manual]
//	synthkit-chat run      -scenario story.chat.yaml -bindings bindings.yaml [-trigger synthkit|manual]
//	synthkit-chat tap      -bindings bindings.yaml -out events.jsonl [-join-match '^incident-']
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/rknightion/synthkit/internal/chatsim/director"
	"github.com/rknightion/synthkit/internal/chatsim/engine"
	"github.com/rknightion/synthkit/internal/chatsim/llm/openrouter"
	"github.com/rknightion/synthkit/internal/chatsim/script"
	"github.com/rknightion/synthkit/internal/chatsim/transport/console"
	slackt "github.com/rknightion/synthkit/internal/chatsim/transport/slack"
	"github.com/rknightion/synthkit/internal/chatsim/trigger/synthkit"
)

const usage = `usage: synthkit-chat <validate|console|run|tap> [flags]
  validate  check a scenario (and optionally its bindings) without connecting anywhere
  console   rehearse in the terminal: you are the operator, :help lists injections
  run       live: Slack Socket Mode, the SynthKit trigger, the /chatsim kill switch
  tap       connect to Slack and append every event envelope to a JSONL file; posts nothing
run "synthkit-chat <command> -h" for flags; docs/chat-scenarios.md has the walkthrough`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "synthkit-chat:", err)
		os.Exit(1)
	}
}

type flags struct {
	fs         *flag.FlagSet
	scenario   string
	bindings   string
	llm        string
	trigger    string
	journal    string
	noColor    bool
	zdr        bool
	model      string
	out        string
	joinMatch  string
	controlURL string
}

func newFlags(name, defLLM, defTrigger string, stderr io.Writer) *flags {
	f := &flags{fs: flag.NewFlagSet(name, flag.ContinueOnError)}
	f.fs.SetOutput(stderr)
	f.fs.StringVar(&f.scenario, "scenario", "", "chat scenario file (*.chat.yaml)")
	f.fs.StringVar(&f.bindings, "bindings", "", "workspace bindings file (channel ids, operator id, IRM ids, secret variable names)")
	f.fs.StringVar(&f.llm, "llm", defLLM, "director backend: openrouter | fake")
	f.fs.StringVar(&f.trigger, "trigger", defTrigger, "run start: synthkit (poll /control/state) | manual (start now) | wait (console: type start or :synthkit on)")
	f.fs.StringVar(&f.journal, "journal", "", "journal file (default: <journal_dir>/<scenario>-<time>.jsonl)")
	f.fs.BoolVar(&f.noColor, "no-color", false, "console: no ANSI colour")
	f.fs.BoolVar(&f.zdr, "zdr", false, "OpenRouter: restrict to zero-data-retention endpoints (also llm.zdr or openrouter.zdr)")
	f.fs.StringVar(&f.model, "model", "", "override llm.model")
	f.fs.StringVar(&f.out, "out", "", "tap: output JSONL file")
	f.fs.StringVar(&f.joinMatch, "join-match", "", "tap: join new channels whose name matches this regex")
	f.fs.StringVar(&f.controlURL, "control-url", "", "override control.url (SynthKit control plane root)")
	return f
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "validate":
		f := newFlags(cmd, "fake", "synthkit", stderr)
		if err := f.fs.Parse(rest); err != nil {
			return err
		}
		return validate(f, stdout)
	case "console":
		f := newFlags(cmd, "fake", "wait", stderr)
		if err := f.fs.Parse(rest); err != nil {
			return err
		}
		return consoleMode(ctx, f, stdin, stdout)
	case "run":
		f := newFlags(cmd, "openrouter", "synthkit", stderr)
		if err := f.fs.Parse(rest); err != nil {
			return err
		}
		return liveMode(ctx, f, stdin, stdout, stderr)
	case "tap":
		f := newFlags(cmd, "fake", "synthkit", stderr)
		if err := f.fs.Parse(rest); err != nil {
			return err
		}
		return tapMode(ctx, f, stderr)
	case "-h", "--help", "help":
		fmt.Fprintln(stdout, usage)
		return nil
	}
	return fmt.Errorf("unknown command %q\n%s", cmd, usage)
}

func loadScenario(f *flags) (*script.Scenario, error) {
	if f.scenario == "" {
		return nil, errors.New("-scenario is required")
	}
	sc, err := script.Load(f.scenario)
	if err != nil {
		return nil, err
	}
	if f.model != "" {
		sc.LLM.Model = f.model
	}
	return sc, nil
}

func validate(f *flags, stdout io.Writer) error {
	sc, err := loadScenario(f)
	if err != nil {
		return err
	}
	if f.bindings != "" {
		b, err := loadBindings(f.bindings)
		if err != nil {
			return err
		}
		if err := b.forScenario(sc); err != nil {
			return err
		}
	}
	fmt.Fprintf(stdout, "ok: %s (%d personas, %d beats, %d facts); trigger %q after %s; model %s\n",
		sc.Name, len(sc.Personas), len(sc.Beats), len(sc.Facts), sc.Trigger.SynthkitScenario, sc.Trigger.Delay.Duration, sc.LLM.Model)
	return nil
}

func openJournal(f *flags, dir, name string) (*engine.Journal, func() error, string, error) {
	path := f.journal
	if path == "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, nil, "", err
		}
		path = filepath.Join(dir, fmt.Sprintf("%s-%s.jsonl", name, time.Now().UTC().Format("20060102T150405Z")))
	}
	fh, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, "", err
	}
	return engine.NewJournal(fh), fh.Close, path, nil
}

func newDirector(f *flags, sc *script.Scenario, b *Bindings) (engine.Director, func() string, error) {
	switch f.llm {
	case "fake":
		fake := &director.FakeLLM{}
		return director.New(sc, fake), func() string { return fmt.Sprintf("fake llm: %d calls", fake.Calls) }, nil
	case "openrouter":
	default:
		return nil, nil, fmt.Errorf("-llm %q: want openrouter or fake", f.llm)
	}
	keyEnv := defaultAPIKeyEnv
	var order []string
	zdr := f.zdr || sc.LLM.ZDR
	if b != nil {
		keyEnv, order, zdr = b.OpenRouter.APIKeyEnv, b.OpenRouter.ProviderOrder, zdr || b.OpenRouter.ZDR
	}
	if len(sc.LLM.ProviderOrder) > 0 {
		order = sc.LLM.ProviderOrder
	}
	key, err := secret(keyEnv)
	if err != nil {
		return nil, nil, err
	}
	seed := int64(sc.Seed)
	c, err := openrouter.New(openrouter.Config{
		APIKey: key, Model: sc.LLM.Model, Temperature: sc.LLM.Temperature, MaxTokens: sc.LLM.MaxTokens,
		Seed: &seed, ReasoningEffort: sc.LLM.ReasoningEffort, ProviderOrder: order, ZDR: zdr, AppTitle: "synthkit-chat",
	})
	if err != nil {
		return nil, nil, err
	}
	return director.New(sc, c), func() string {
		u := c.Usage()
		return fmt.Sprintf("openrouter %s: %d calls, %d prompt + %d completion tokens", sc.LLM.Model, u.Calls, u.PromptTokens, u.CompletionTokens)
	}, nil
}

func firstStatic(sc *script.Scenario) string {
	for _, id := range sc.ChannelIDs() {
		if sc.Channels[id].Bind == nil {
			return id
		}
	}
	return ""
}

func consoleMode(ctx context.Context, f *flags, stdin io.Reader, stdout io.Writer) error {
	sc, err := loadScenario(f)
	if err != nil {
		return err
	}
	var b *Bindings
	if f.bindings != "" {
		if b, err = loadBindings(f.bindings); err != nil {
			return err
		}
	}
	dir, usageFn, err := newDirector(f, sc, b)
	if err != nil {
		return err
	}
	jdir := defaultJournalDir
	if b != nil {
		jdir = b.JournalDir
	}
	j, closeJ, jpath, err := openJournal(f, jdir, sc.Name)
	if err != nil {
		return err
	}
	defer closeJ()
	channels := map[string]string{}
	for _, id := range sc.ChannelIDs() {
		if sc.Channels[id].Bind == nil {
			channels[id] = id
		}
	}
	m, err := engine.NewMachine(sc, engine.Options{Channels: channels, OperatorID: "console-operator", Journal: j})
	if err != nil {
		return err
	}
	clock := engine.RealClock{}
	tr := &console.Transport{Out: stdout, Color: !f.noColor, Clock: clock}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	events := make(chan engine.Event, 64)
	reader := &console.Reader{In: stdin, Out: tr, Channel: firstStatic(sc), Operator: sc.Operator.Name, Scenario: sc.Trigger.SynthkitScenario}
	go func() {
		// End of input (EOF or :quit) stops the run in order, after the lines already typed.
		_ = reader.Run(ctx, events)
		select {
		case events <- engine.Event{Kind: engine.EvControl, Command: "stop", Reply: tr.Say}:
		case <-ctx.Done():
		}
	}()
	if err := startTrigger(ctx, f, sc, b, events, tr.Say); err != nil {
		return err
	}
	tr.Say(fmt.Sprintf("console: %s, you are %s in #%s; journal %s. Type :help.", sc.Name, sc.Operator.Name, reader.Channel, jpath))
	err = engine.Run(ctx, m, clock, events, dir, tr)
	tr.Say(fmt.Sprintf("console: run ended (%s); %s", m.EndReason(), usageFn()))
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func startTrigger(ctx context.Context, f *flags, sc *script.Scenario, b *Bindings, events chan<- engine.Event, logf func(string)) error {
	switch f.trigger {
	case "wait":
		return nil
	case "manual":
		events <- engine.Event{Kind: engine.EvControl, Command: "start", Reply: logf}
		return nil
	case "synthkit":
	default:
		return fmt.Errorf("-trigger %q: want synthkit, manual or wait", f.trigger)
	}
	if sc.Trigger.SynthkitScenario == "" {
		return errors.New("-trigger synthkit needs trigger.synthkit_scenario in the scenario")
	}
	url, tokenEnv, iv := f.controlURL, defaultControlEnv, time.Second
	if b != nil {
		if url == "" {
			url = b.Control.URL
		}
		tokenEnv, iv = b.Control.TokenEnv, b.Control.PollInterval.Duration
	}
	if url == "" {
		return errors.New("-trigger synthkit needs control.url in bindings or -control-url")
	}
	token := os.Getenv(tokenEnv) // an unauthenticated lab control plane is allowed
	p := &synthkit.Poller{BaseURL: url, Token: token, Scenario: sc.Trigger.SynthkitScenario, Interval: iv,
		Logf: func(format string, a ...any) { logf(fmt.Sprintf(format, a...)) }}
	go func() { _ = p.Run(ctx, events) }()
	return nil
}

func liveMode(ctx context.Context, f *flags, stdin io.Reader, stdout, stderr io.Writer) error {
	sc, err := loadScenario(f)
	if err != nil {
		return err
	}
	if f.bindings == "" {
		return errors.New("run needs -bindings")
	}
	b, err := loadBindings(f.bindings)
	if err != nil {
		return err
	}
	if err := b.forScenario(sc); err != nil {
		return err
	}
	dir, usageFn, err := newDirector(f, sc, b)
	if err != nil {
		return err
	}
	bot, err := secret(b.Slack.BotTokenEnv)
	if err != nil {
		return err
	}
	app, err := secret(b.Slack.AppTokenEnv)
	if err != nil {
		return err
	}
	j, closeJ, jpath, err := openJournal(f, b.JournalDir, sc.Name)
	if err != nil {
		return err
	}
	defer closeJ()
	logTo := &console.Transport{Out: stdout, Clock: engine.RealClock{}}
	logf := func(format string, a ...any) { logTo.Say(fmt.Sprintf(format, a...)) }
	tr, err := slackt.New(ctx, slackt.Config{BotToken: bot, AppToken: app, OperatorID: b.OperatorUserID,
		IRMBotIDs: b.IRM.BotIDs, IRMAppIDs: b.IRM.AppIDs, Logf: logf})
	if err != nil {
		return err
	}
	m, err := engine.NewMachine(sc, engine.Options{Channels: b.Channels, OperatorID: b.OperatorUserID, Journal: j})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	events := make(chan engine.Event, 256)
	go func() { cancel(tr.Run(ctx, events)) }()
	reader := &console.Reader{In: stdin, Out: logTo, ControlOnly: true}
	go func() { _ = reader.Run(ctx, events) }()
	if err := startTrigger(ctx, f, sc, b, events, logTo.Say); err != nil {
		return err
	}
	logf("run: %s armed on %q (+%s); journal %s; /chatsim or stdin: pause | resume | stop | status | skip <beat> | say <persona> <text>",
		sc.Name, sc.Trigger.SynthkitScenario, sc.Trigger.Delay.Duration, jpath)
	err = engine.Run(ctx, m, engine.RealClock{}, events, dir, tr)
	logf("run: ended (%s); %s", m.EndReason(), usageFn())
	if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) {
		return cause
	}
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func tapMode(ctx context.Context, f *flags, stderr io.Writer) error {
	if f.bindings == "" || f.out == "" {
		return errors.New("tap needs -bindings and -out")
	}
	b, err := loadBindings(f.bindings)
	if err != nil {
		return err
	}
	var join *regexp.Regexp
	if f.joinMatch != "" {
		if join, err = regexp.Compile(f.joinMatch); err != nil {
			return fmt.Errorf("-join-match: %w", err)
		}
	}
	bot, err := secret(b.Slack.BotTokenEnv)
	if err != nil {
		return err
	}
	app, err := secret(b.Slack.AppTokenEnv)
	if err != nil {
		return err
	}
	fh, err := os.OpenFile(f.out, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer fh.Close()
	logf := func(format string, a ...any) { fmt.Fprintf(stderr, format+"\n", a...) }
	tr, err := slackt.New(ctx, slackt.Config{BotToken: bot, AppToken: app, OperatorID: b.OperatorUserID,
		IRMBotIDs: b.IRM.BotIDs, IRMAppIDs: b.IRM.AppIDs, Logf: logf, Tap: fh})
	if err != nil {
		return err
	}
	events := make(chan engine.Event, 256)
	errc := make(chan error, 1)
	go func() { errc <- tr.Run(ctx, events) }()
	logf("tap: writing every envelope to %s; ctrl-c to stop", f.out)
	for {
		select {
		case err := <-errc:
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		case ev := <-events:
			logf("tap: %s %s from=%s %q", ev.Kind, ev.ChannelID, ev.From, strings.SplitN(ev.Text, "\n", 2)[0])
			if ev.Kind == engine.EvChannelCreated && join != nil && join.MatchString(ev.ChannelName) {
				go func(id string) {
					if err := tr.Join(ctx, id); err != nil {
						logf("tap: %v", err)
					}
				}(ev.ChannelID)
			}
		}
	}
}
