// SPDX-License-Identifier: AGPL-3.0-only

// agento11y-provision is a source-built, same-UID local manual management
// command. It does not install runtime admission, start emitters or support a
// container/HA handoff. No environment credential or dotenv is read.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	p "github.com/rknightion/synthkit/internal/agento11yprovision"
	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/runner"
	"golang.org/x/sys/unix"
)

const invocationTimeout = 10 * time.Minute
const inputLimit = 786432
const privateLimit = 65536
const tokenLimit = 16384

func main() {
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, invocationTimeout)
	defer cancel()
	result := make(chan int, 1)
	go func() { result <- runArgs(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr) }()
	select {
	case code := <-result:
		os.Exit(code)
	case <-ctx.Done():
		// Hard process boundary, not a cancellation/noncommit/join receipt. Pending
		// durable evidence is left intact; never release authority around live work.
		fmt.Fprintln(os.Stderr, `{"reason":"unknown_deadline"}`)
		os.Exit(4)
	}
}

type repeated []string

func (r *repeated) String() string     { return "" }
func (r *repeated) Set(v string) error { *r = append(*r, v); return nil }

type arguments struct {
	verb                                                              string
	files, namespaces                                                 repeated
	binding, state, preview, approval, construction, mode, out, token string
	enrolled, tokenStdin, help, version                               bool
	seen                                                              map[string]bool
}

func parseArgs(args []string) (arguments, error) {
	a := arguments{verb: "plan", mode: "apply", seen: map[string]bool{}}
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		a.verb = args[0]
		args = args[1:]
	}
	fs := flag.NewFlagSet("agento11y-provision", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Var(&a.files, "blueprint-file", "explicit local blueprint; repeat with namespace")
	fs.Var(&a.namespaces, "blueprint-namespace", "paired namespace; empty means plain Load")
	fs.StringVar(&a.binding, "binding-file", "", "private custody Binding artifact")
	fs.StringVar(&a.state, "state-dir", "", "same physical private local authority")
	fs.BoolVar(&a.enrolled, "expect-enrolled", false, "retained external enrollment expectation")
	fs.StringVar(&a.preview, "preview-file", "", "original private preview")
	fs.StringVar(&a.approval, "approval-file", "", "separately reviewed private approval")
	fs.StringVar(&a.construction, "construction-id", "", "exact historical construction")
	fs.StringVar(&a.mode, "mode", "apply", "preview only: apply/recover/deactivate/teardown")
	fs.StringVar(&a.out, "out", "", "exclusive new private output")
	fs.StringVar(&a.token, "token-file", "", "private management bearer file")
	fs.BoolVar(&a.tokenStdin, "token-stdin", false, "read management bearer from stdin only")
	fs.BoolVar(&a.help, "help", false, "local manual command help")
	fs.BoolVar(&a.version, "version", false, "source-build metadata only")
	if fs.Parse(args) != nil || fs.NArg() != 0 {
		return a, p.ErrInvalid
	}
	fs.Visit(func(f *flag.Flag) { a.seen[f.Name] = true })
	// Scalar repetitions are ambiguous authority; repeated file/namespace pairs
	// are the sole repeatable flags. flag itself otherwise silently takes last.
	counts := map[string]int{}
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			k := strings.TrimLeft(strings.SplitN(arg, "=", 2)[0], "-")
			if a.seen[k] {
				counts[k]++
			}
		}
	}
	for k, n := range counts {
		if n > 1 && k != "blueprint-file" && k != "blueprint-namespace" {
			return a, p.ErrInvalid
		}
	}
	if a.help || a.version {
		if len(a.seen) != 1 || a.verb != "plan" {
			return a, p.ErrInvalid
		}
		return a, nil
	}
	if a.seen["help"] || a.seen["version"] {
		return a, p.ErrInvalid
	}
	allowed := map[string]bool{"binding-file": true}
	require := []string{"binding-file"}
	files := func() {
		allowed["blueprint-file"] = true
		allowed["blueprint-namespace"] = true
		require = append(require, "blueprint-file", "blueprint-namespace")
	}
	online := func() {
		for _, k := range []string{"state-dir", "expect-enrolled", "token-file", "token-stdin"} {
			allowed[k] = true
		}
		require = append(require, "state-dir")
	}
	history := func() {
		allowed["construction-id"] = true
		require = append(require, "construction-id", "expect-enrolled")
	}
	switch a.verb {
	case "plan":
		files()
		allowed["out"] = true
	case "preview":
		online()
		allowed["out"] = true
		allowed["mode"] = true
		require = append(require, "out")
		switch p.Mode(a.mode) {
		case p.ModeApply:
			files()
		case p.ModeRecover, p.ModeDeactivate, p.ModeTeardown:
			history()
		default:
			return a, p.ErrInvalid
		}
	case "apply":
		files()
		online()
		for _, k := range []string{"preview-file", "approval-file"} {
			allowed[k] = true
			require = append(require, k)
		}
	case "recover", "deactivate", "teardown":
		online()
		history()
		for _, k := range []string{"preview-file", "approval-file"} {
			allowed[k] = true
			require = append(require, k)
		}
	default:
		return a, p.ErrInvalid
	}
	for k := range a.seen {
		if !allowed[k] {
			return a, p.ErrInvalid
		}
	}
	for _, k := range require {
		if !a.seen[k] {
			return a, p.ErrInvalid
		}
	}
	if len(a.files) != len(a.namespaces) || a.seen["blueprint-file"] && len(a.files) == 0 {
		return a, p.ErrInvalid
	}
	for _, s := range a.files {
		if s == "" {
			return a, p.ErrInvalid
		}
	}
	for _, ns := range a.namespaces {
		if ns != "" {
			for _, r := range ns {
				if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
					return a, p.ErrInvalid
				}
			}
		}
	}
	if a.binding == "" || a.seen["out"] && a.out == "" || a.seen["state-dir"] && a.state == "" || a.seen["preview-file"] && a.preview == "" || a.seen["approval-file"] && a.approval == "" || a.seen["construction-id"] && a.construction == "" || a.seen["expect-enrolled"] && !a.enrolled {
		return a, p.ErrInvalid
	}
	if a.verb != "plan" && (a.seen["token-file"] == a.seen["token-stdin"] || a.seen["token-file"] && a.token == "" || a.seen["token-stdin"] && !a.tokenStdin) {
		return a, p.ErrInvalid
	}
	return a, nil
}

// Private reads use an opened non-following fd before inspecting owner/mode,
// size and type. Never open a FIFO in blocking mode or chmod foreign inputs.
func readInput(path string, limit int, private bool) ([]byte, error) {
	parent, leaf, e := privateParent(path)
	if e != nil {
		return nil, e
	}
	defer parent.Close()
	fd, e := unix.Openat(int(parent.Fd()), leaf, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if e != nil {
		return nil, p.ErrInvalid
	}
	f := os.NewFile(uintptr(fd), "private-input")
	defer f.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || uint32(st.Mode)&unix.S_IFMT != unix.S_IFREG || st.Size < 0 || st.Size > int64(limit) {
		return nil, p.ErrInvalid
	}
	if private && (st.Uid != uint32(os.Geteuid()) || st.Mode&0077 != 0 || st.Mode&07000 != 0 || st.Nlink != 1) {
		return nil, p.ErrInvalid
	}
	raw, e := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if e != nil || len(raw) > limit {
		return nil, p.ErrCapacity
	}
	return raw, nil
}
func readCredential(ctx context.Context, r io.Reader) ([]byte, error) {
	type result struct {
		raw []byte
		err error
	}
	done := make(chan result, 1)
	go func() { b, e := io.ReadAll(io.LimitReader(r, tokenLimit+1)); done <- result{b, e} }()
	select {
	case v := <-done:
		if v.err != nil || len(v.raw) > tokenLimit {
			return nil, p.ErrInvalid
		}
		return v.raw, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func outputPath(a arguments) error {
	if a.out == "" {
		return nil
	}
	out, e := filepath.Abs(a.out)
	if e != nil {
		return p.ErrInvalid
	}
	// Resolve parents to catch alternate directory spellings; the final leaf
	// must not exist at all, including dangling symlinks and hardlink aliases.
	parent, e := filepath.EvalSymlinks(filepath.Dir(out))
	if e != nil {
		return p.ErrInvalid
	}
	out = filepath.Join(parent, filepath.Base(out))
	if _, e := os.Lstat(out); !os.IsNotExist(e) {
		return p.ErrInvalid
	}
	inputs := append([]string{a.binding, a.preview, a.approval, a.token}, a.files...)
	for _, path := range inputs {
		if path == "" {
			continue
		}
		in, e := filepath.EvalSymlinks(path)
		if e != nil {
			return p.ErrInvalid
		}
		in, e = filepath.Abs(in)
		if e != nil || in == out {
			return p.ErrInvalid
		}
	}
	if a.state != "" {
		dir, e := filepath.EvalSymlinks(a.state)
		if e != nil {
			return p.ErrInvalid
		}
		dir, e = filepath.Abs(dir)
		if e != nil || out == dir || filepath.Dir(out) == dir {
			return p.ErrInvalid
		}
	}
	return nil
}

// Walk opened directory descriptors, not path strings, so a symlink parent or
// a parent rename cannot redirect private input/output after validation. This
// is file handling only; B alone owns state/lock identity and enrollment.
func privateParent(path string) (*os.File, string, error) {
	abs, e := filepath.Abs(path)
	if e != nil || abs == "/" {
		return nil, "", p.ErrInvalid
	}
	flags := unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	fd, e := unix.Open("/", flags, 0)
	if e != nil {
		return nil, "", p.ErrIO
	}
	parts := strings.Split(abs[1:], "/")
	for _, part := range parts[:len(parts)-1] {
		var st unix.Stat_t
		if unix.Fstat(fd, &st) != nil || st.Uid != 0 && st.Uid != uint32(os.Geteuid()) || st.Mode&0022 != 0 && !(st.Uid == 0 && st.Mode&unix.S_ISVTX != 0) {
			unix.Close(fd)
			return nil, "", p.ErrInvalid
		}
		next, err := unix.Openat(fd, part, flags, 0)
		unix.Close(fd)
		if err != nil {
			return nil, "", p.ErrInvalid
		}
		fd = next
	}
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != 0 && st.Uid != uint32(os.Geteuid()) || st.Mode&0022 != 0 && !(st.Uid == 0 && st.Mode&unix.S_ISVTX != 0) {
		unix.Close(fd)
		return nil, "", p.ErrInvalid
	}
	return os.NewFile(uintptr(fd), "private-parent"), parts[len(parts)-1], nil
}

func writePrivate(path string, raw []byte) error {
	dir, leaf, e := privateParent(path)
	if e != nil {
		return e
	}
	defer dir.Close()
	fd, e := unix.Openat(int(dir.Fd()), leaf, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return p.ErrIO
	}
	f := os.NewFile(uintptr(fd), "private-output")
	_, e = f.Write(raw)
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil || closeErr != nil {
		return p.ErrOutcomeUnknown
	}
	e = dir.Sync()
	closeErr = dir.Close()
	if e != nil || closeErr != nil {
		return p.ErrOutcomeUnknown
	}
	return nil
}
func refused(stderr io.Writer, err error) int {
	code := 2
	reason := "refused"
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.Is(err, p.ErrOutcomeUnknown) {
		code = 4
		reason = "unknown_deadline"
	}
	fmt.Fprintf(stderr, "{\"reason\":%q}\n", reason)
	return code
}

// uncertain reports a failure after custody changed or with an outcome that
// cannot be classified: preview again and follow its next step.
func uncertain(stderr io.Writer) int {
	fmt.Fprintln(stderr, `{"reason":"custody_changed_or_outcome_uncertain","next":"preview"}`)
	return 4
}

// custodyUnchanged rereads custody under the still-held lock. It is true only
// when the head (generation and checksum, or pristine absence) equals the head
// read before the engine call and no operation is pending. Any reread failure,
// including an expired invocation deadline, is not proof of no change.
func custodyUnchanged(ctx context.Context, locked *p.LockedState, before p.Snapshot) bool {
	after, e := locked.Load(ctx)
	if e != nil || after.Base.Pristine != before.Base.Pristine || (after.Base.Head == nil) != (before.Base.Head == nil) {
		return false
	}
	if after.Base.Head != nil && *after.Base.Head != *before.Base.Head {
		return false
	}
	return after.Document == nil || after.Document.Pending == nil
}
func summary(w io.Writer, v any) error { return json.NewEncoder(w).Encode(v) }
func runArgs(parent context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	ctx, cancel := context.WithTimeout(parent, invocationTimeout)
	defer cancel()
	a, e := parseArgs(args)
	if e != nil {
		return refused(stderr, e)
	}
	if a.help {
		fmt.Fprintln(stdout, "agento11y-provision: source-built same-UID local manual management only; no runtime admission or container/HA support.\nVerbs: plan (default), preview, apply, recover, deactivate, teardown.\nFlags: --blueprint-file PATH --blueprint-namespace NS (repeat paired; empty NS allowed), --binding-file PATH, --state-dir PATH, --expect-enrolled, --preview-file PATH, --approval-file PATH, --construction-id ID, --mode apply|recover|deactivate|teardown (preview only), --out PATH, --token-file PATH | --token-stdin, --help, --version.\nOnly private inputs; no token/URL/tenant argv or ambient credentials. Preview requires a new private --out; plan is offline. Operator stop/exclusivity assumptions are not backend fencing, drain, noncommit or anti-rollback.")
		return 0
	}
	if a.version {
		revision := "unknown"
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, s := range info.Settings {
				if s.Key == "vcs.revision" {
					revision = s.Value
				}
			}
		}
		if summary(stdout, map[string]string{"distribution": "source-built-local-manual", "revision": revision}) != nil {
			return 4
		}
		return 0
	}
	if e = ctx.Err(); e != nil {
		return refused(stderr, e)
	}
	if e = outputPath(a); e != nil {
		return refused(stderr, e)
	}
	raw, e := readInput(a.binding, privateLimit, true)
	if e != nil {
		return refused(stderr, e)
	}
	binding, e := p.DecodeBinding(raw)
	if e != nil {
		return refused(stderr, e)
	}
	var in p.PlanInput
	if len(a.files) > 0 {
		reg := runner.Catalog()
		set := []*blueprint.Resolved{}
		total := 0
		for i, path := range a.files {
			data, e := readInput(path, inputLimit, false)
			if e != nil {
				return refused(stderr, e)
			}
			if len(data) > inputLimit-total {
				return refused(stderr, p.ErrCapacity)
			}
			total += len(data)
			res, e := blueprint.LoadNamespaced(data, a.namespaces[i], reg)
			if e != nil {
				return refused(stderr, e)
			}
			set = append(set, res)
		}
		in, e = runner.RegistrationPlan(reg, set, binding)
		if e != nil {
			return refused(stderr, e)
		}
	}
	if a.verb == "plan" {
		prepared, e := p.Prepare(in)
		if e != nil {
			return refused(stderr, e)
		}
		result := map[string]any{"input": prepared.Digest(), "resources": len(in.Resources), "claims": len(in.Targets), "runtime_admission": false}
		if a.out != "" {
			raw, e := json.Marshal(result)
			if e == nil {
				e = writePrivate(a.out, append(raw, '\n'))
			}
			if e != nil {
				return refused(stderr, e)
			}
		} else if summary(stdout, result) != nil {
			return 4
		}
		return 0
	}
	// Read every bounded private artifact and credential before acquiring any
	// authority. C alone reobserves and authorizes after the lock is held.
	var previewRaw, approvalRaw []byte
	if a.preview != "" {
		previewRaw, e = readInput(a.preview, inputLimit, true)
		if e != nil {
			return refused(stderr, e)
		}
	}
	if a.approval != "" {
		approvalRaw, e = readInput(a.approval, privateLimit, true)
		if e != nil {
			return refused(stderr, e)
		}
	}
	var token []byte
	if a.tokenStdin {
		token, e = readCredential(ctx, stdin)
	} else {
		token, e = readInput(a.token, tokenLimit, true)
	}
	if e != nil {
		return refused(stderr, e)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	client, e := p.NewClient(p.ClientOptions{BaseURL: binding.Destination.GrafanaOrigin + binding.Destination.DeploymentPath, Token: string(token), ExpectedTenantID: binding.Destination.EffectiveTenant, Timeout: 15 * time.Second, HTTPClient: &http.Client{Transport: transport}})
	if e != nil {
		return refused(stderr, e)
	}
	defer transport.CloseIdleConnections()
	locked, e := p.AcquireState(ctx, p.Location{Directory: a.state, ExpectEnrolled: a.enrolled}, binding)
	if errors.Is(e, p.ErrLockTimeout) && ctx.Err() == nil {
		// The bounded lock wait expired while another holder kept authority:
		// nothing was attempted, so this is a plain refusal, not an unknown.
		fmt.Fprintln(stderr, `{"reason":"lock_timeout"}`)
		return 2
	}
	if e != nil {
		return refused(stderr, e)
	}
	// All HTTP/state work below is synchronous and joined before Close. A hard
	// main deadline terminates the process instead of unlocking around work.
	defer locked.Close()
	if len(a.files) == 0 {
		snap, e := locked.Load(ctx)
		if e != nil {
			return refused(stderr, e)
		}
		if snap.Document == nil {
			return refused(stderr, p.ErrEnrolledMissing)
		}
		mode := p.Mode(a.verb)
		if a.verb == "preview" {
			mode = p.Mode(a.mode)
		}
		in, e = runner.RegistrationHistoryPlan(*snap.Document, a.construction, mode)
		if e != nil {
			return refused(stderr, e)
		}
	}
	prepared, e := p.Prepare(in)
	if e != nil {
		return refused(stderr, e)
	}
	if a.verb == "preview" {
		v, e := p.PreviewPrepared(ctx, client, locked, prepared, p.Mode(a.mode))
		if e != nil {
			return refused(stderr, e)
		}
		if e = writePrivate(a.out, v.JSON()); e != nil {
			return refused(stderr, e)
		}
		if e = locked.Close(); e != nil {
			return refused(stderr, e)
		}
		if summary(stdout, map[string]any{"preview": v.Digest(), "input": prepared.Digest(), "resources": len(in.Resources), "runtime_admission": false}) != nil {
			return 4
		}
		return 0
	}
	v, e := p.DecodePreparedPreview(prepared, previewRaw)
	if e != nil {
		return refused(stderr, e)
	}
	approval, e := p.DecodeApproval(approvalRaw)
	if e != nil {
		return refused(stderr, e)
	}
	if a.verb == "recover" {
		// DecodeApproval has already strictly validated the existing wire. This
		// projection only enforces the public selector; it creates no authority.
		var selected struct {
			ConstructionID string `json:"construction_id"`
		}
		if json.Unmarshal(approvalRaw, &selected) != nil || selected.ConstructionID != a.construction {
			return refused(stderr, p.ErrConflict)
		}
	}
	// Custody head before authority is exercised. A failure exits 2 only when
	// the head is provably unchanged and nothing is pending; otherwise 4.
	before, e := locked.Load(ctx)
	if e != nil {
		return refused(stderr, e)
	}
	var receipt p.Receipt
	switch a.verb {
	case "apply":
		receipt, e = p.ApplyPrepared(ctx, client, locked, prepared, v, approval)
	case "recover":
		receipt, e = p.RecoverPrepared(ctx, client, locked, prepared, v, approval)
	default:
		receipt, e = p.CleanupPrepared(ctx, client, locked, prepared, v, approval)
	}
	if receipt.Result == p.ReceiptIncomplete {
		if closeErr := locked.Close(); closeErr != nil {
			// After a mutation attempt a release failure is never a clean refusal.
			return refused(stderr, p.ErrOutcomeUnknown)
		}
		if summary(stdout, map[string]any{"input": receipt.Input, "result": receipt.Result, "members": len(receipt.Members), "runtime_admission": false}) != nil {
			return 4
		}
		return 3
	}
	if e == nil && receipt.Result == "" {
		e = p.ErrConflict
	}
	if e != nil {
		if !custodyUnchanged(ctx, locked, before) {
			return uncertain(stderr)
		}
		return refused(stderr, e)
	}
	if e = locked.Close(); e != nil {
		return refused(stderr, p.ErrOutcomeUnknown)
	}
	if summary(stdout, map[string]any{"input": receipt.Input, "result": receipt.Result, "members": len(receipt.Members), "runtime_admission": false}) != nil {
		return 4
	}
	return 0
}
