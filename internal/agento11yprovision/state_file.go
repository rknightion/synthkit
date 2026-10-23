// SPDX-License-Identifier: AGPL-3.0-only

//go:build darwin || linux

package agento11yprovision

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

type stateEdges struct {
	// Private, per-handle process edges, never production options/global hooks.
	flock func(int) error
	sync  func(int, string) error
	after func(string) error
}
type LockedState struct {
	mu               sync.Mutex
	self             *LockedState
	pid              int
	dir, lock        *os.File
	location         Location
	binding          Binding
	dirID, lockID    [2]uint64
	ctx              context.Context
	cancel           context.CancelFunc
	closed, poisoned bool
	grant            *DispatchGrant
	now              func() time.Time
	lastWall         int64
	edges            stateEdges
}
type Authorization struct {
	self                     *Authorization
	owner                    *LockedState
	approval                 Approval
	preview                  Preview
	chain                    Base
	inventory                Digest
	construction             string
	started, finished        bool
	dispatchable, dispatched map[string]bool
}
type DispatchGrant struct {
	self     *DispatchGrant
	owner    *LockedState
	attempt  string
	ctx      context.Context
	cancel   context.CancelFunc
	released bool
}

func custodyFileID(st unix.Stat_t) [2]uint64 { return [2]uint64{uint64(st.Dev), uint64(st.Ino)} }
func custodyStat(fd int) (unix.Stat_t, error) {
	var st unix.Stat_t
	e := unix.Fstat(fd, &st)
	return st, e
}
func custodyPrivate(st unix.Stat_t, dir bool) bool {
	kind := uint32(unix.S_IFREG)
	mode := uint32(0600)
	if dir {
		kind = unix.S_IFDIR
		mode = 0700
	}
	return uint32(st.Mode)&unix.S_IFMT == kind && st.Uid == uint32(os.Geteuid()) && uint32(st.Mode)&07777 == mode && (dir || st.Nlink == 1)
}
func custodyWalk(path string) (*os.File, [2]uint64, error) {
	var zero [2]uint64
	if path == "/" || !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.Contains(path, "//") {
		return nil, zero, ErrInvalid
	}
	flags := unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	fd, e := unix.Open("/", flags, 0)
	if e != nil {
		return nil, zero, ErrIO
	}
	parts := strings.Split(path[1:], "/")
	for i, p := range parts {
		st, e := custodyStat(fd)
		if e != nil || st.Uid != 0 && st.Uid != uint32(os.Geteuid()) || st.Mode&0022 != 0 && !(st.Uid == 0 && st.Mode&unix.S_ISVTX != 0) {
			unix.Close(fd)
			return nil, zero, ErrInvalid
		}
		next, e := unix.Openat(fd, p, flags, 0)
		unix.Close(fd)
		if e != nil {
			return nil, zero, ErrInvalid
		}
		fd = next
		if i == len(parts)-1 {
			st, e = custodyStat(fd)
			if e != nil || !custodyPrivate(st, true) {
				unix.Close(fd)
				return nil, zero, ErrInvalid
			}
			return os.NewFile(uintptr(fd), "custody-directory"), custodyFileID(st), nil
		}
	}
	unix.Close(fd)
	return nil, zero, ErrInvalid
}
func AcquireState(ctx context.Context, location Location, binding Binding) (*LockedState, error) {
	return acquireCustody(ctx, location, binding, stateEdges{})
}
func acquireCustody(ctx context.Context, location Location, binding Binding, edges stateEdges) (*LockedState, error) {
	if ctx == nil || !custodyBinding(binding) {
		return nil, ErrInvalid
	}
	inv, cancel := context.WithTimeout(ctx, 10*time.Minute)
	dir, id, e := custodyWalk(location.Directory)
	if e != nil {
		cancel()
		return nil, e
	}
	fd, e := unix.Openat(int(dir.Fd()), "lock", unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0600)
	if e != nil {
		dir.Close()
		cancel()
		return nil, ErrIO
	}
	lock := os.NewFile(uintptr(fd), "custody-lock")
	fail := func(e error) (*LockedState, error) { lock.Close(); dir.Close(); cancel(); return nil, e }
	st, e := custodyStat(fd)
	if e != nil || !custodyPrivate(st, false) || st.Size != 0 {
		return fail(ErrInvalid)
	}
	wait, stop := context.WithTimeout(inv, 5*time.Second)
	defer stop()
	for {
		if e = wait.Err(); e != nil {
			return fail(errors.Join(ErrLockTimeout, e))
		}
		if edges.flock != nil {
			e = edges.flock(fd)
		} else {
			e = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		}
		if e == nil {
			break
		}
		if e != unix.EWOULDBLOCK && e != unix.EAGAIN {
			return fail(ErrIO)
		}
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-timer.C:
		case <-wait.Done():
			timer.Stop()
			return fail(errors.Join(ErrLockTimeout, wait.Err()))
		}
	}
	s := &LockedState{pid: os.Getpid(), dir: dir, lock: lock, location: location, binding: binding, dirID: id, lockID: custodyFileID(st), ctx: inv, cancel: cancel, now: time.Now, edges: edges}
	s.self = s
	if e = s.identity(); e != nil {
		return fail(e)
	}
	return s, nil
}
func (s *LockedState) identity() error {
	if s == nil || s.self != s || s.pid != os.Getpid() {
		return ErrInvalid
	}
	if s.closed {
		return ErrClosed
	}
	f, id, e := custodyWalk(s.location.Directory)
	if e != nil {
		return e
	}
	f.Close()
	if id != s.dirID {
		return ErrConflict
	}
	st, e := custodyStat(int(s.dir.Fd()))
	if e != nil || !custodyPrivate(st, true) || custodyFileID(st) != s.dirID {
		return ErrConflict
	}
	st, e = custodyStat(int(s.lock.Fd()))
	if e != nil || !custodyPrivate(st, false) || custodyFileID(st) != s.lockID || st.Size != 0 {
		return ErrConflict
	}
	var named unix.Stat_t
	if unix.Fstatat(int(s.dir.Fd()), "lock", &named, unix.AT_SYMLINK_NOFOLLOW) != nil || custodyFileID(named) != s.lockID || !custodyPrivate(named, false) {
		return ErrConflict
	}
	return nil
}
func (s *LockedState) enter(ctx context.Context, change bool) error {
	if s == nil || s.self != s || s.pid != os.Getpid() {
		return ErrInvalid
	}
	if !s.mu.TryLock() {
		return ErrBusy
	}
	fail := func(e error) error { s.mu.Unlock(); return e }
	if e := s.identity(); e != nil {
		return fail(e)
	}
	if change && s.poisoned {
		return fail(ErrOutcomeUnknown)
	}
	if ctx == nil {
		return fail(ErrInvalid)
	}
	if e := ctx.Err(); e != nil {
		return fail(e)
	}
	if e := s.ctx.Err(); e != nil {
		return fail(e)
	}
	n := s.now().UnixMilli()
	if n < s.lastWall {
		return fail(ErrExpired)
	}
	s.lastWall = n
	return nil
}
func (s *LockedState) read(name string, limit int) ([]byte, error) {
	fd, e := unix.Openat(int(s.dir.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if e == unix.ENOENT {
		return nil, os.ErrNotExist
	}
	if e != nil {
		return nil, ErrCorrupt
	}
	f := os.NewFile(uintptr(fd), "custody-record")
	defer f.Close()
	st, e := custodyStat(fd)
	if e != nil || !custodyPrivate(st, false) {
		return nil, ErrCorrupt
	}
	if st.Size > int64(limit) {
		return nil, ErrCapacity
	}
	b, e := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if e != nil {
		return nil, ErrIO
	}
	if len(b) > limit {
		return nil, ErrCapacity
	}
	var named unix.Stat_t
	if unix.Fstatat(int(s.dir.Fd()), name, &named, unix.AT_SYMLINK_NOFOLLOW) != nil || custodyFileID(named) != custodyFileID(st) || !custodyPrivate(named, false) {
		return nil, ErrConflict
	}
	return b, nil
}
func (s *LockedState) inventory() error {
	fd, e := unix.Openat(int(s.dir.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return ErrIO
	}
	f := os.NewFile(uintptr(fd), "custody-inventory")
	defer f.Close()
	entries, e := f.ReadDir(5)
	if e != nil && !errors.Is(e, io.EOF) {
		return ErrIO
	}
	for _, entry := range entries {
		if !slices.Contains([]string{"lock", "enrollment.json", "state.json"}, entry.Name()) {
			if strings.HasPrefix(entry.Name(), ".pending-") {
				return ErrOutcomeUnknown
			}
			return ErrCorrupt
		}
	}
	return nil
}
func (s *LockedState) load() (Snapshot, error) {
	if e := s.inventory(); e != nil {
		return Snapshot{}, e
	}
	eraw, ee := s.read("enrollment.json", custodySpecBytes)
	raw, e := s.read("state.json", custodyStateLimit(s.binding))
	emissing := errors.Is(ee, os.ErrNotExist)
	missing := errors.Is(e, os.ErrNotExist)
	if ee != nil && !emissing {
		return Snapshot{}, ee
	}
	if e != nil && !missing {
		return Snapshot{}, e
	}
	if s.location.ExpectEnrolled && (emissing || missing) {
		return Snapshot{}, ErrEnrolledMissing
	}
	if emissing && missing {
		return Snapshot{Base: Base{Pristine: true}}, nil
	}
	if emissing {
		return Snapshot{}, ErrCorrupt
	}
	if missing {
		return Snapshot{}, ErrEnrolledMissing
	}
	var ew enrollmentWire
	if e = custodyDecode(eraw, &ew, custodySpecBytes); e != nil {
		return Snapshot{}, ErrCorrupt
	}
	h := ew.Checksum
	ew.Checksum = ""
	pre, _ := json.Marshal(ew)
	if ew.Schema != 1 || !custodyDigest(h) || custodyHash("enrollment", pre) != h {
		return Snapshot{}, ErrCorrupt
	}
	if bindingFromWire(ew.Binding) != s.binding {
		return Snapshot{}, ErrBinding
	}
	d, e := custodyDocumentDecode(raw)
	if e != nil {
		return Snapshot{}, e
	}
	if d.Binding != s.binding {
		return Snapshot{}, ErrBinding
	}
	if e = s.identity(); e != nil {
		return Snapshot{}, e
	}
	head := Head{d.Generation, d.Checksum}
	return Snapshot{Base{Head: &head}, &d}, nil
}
func (s *LockedState) Load(ctx context.Context) (Snapshot, error) {
	if e := s.enter(ctx, false); e != nil {
		return Snapshot{}, e
	}
	defer s.mu.Unlock()
	return s.load()
}
func (s *LockedState) after(stage string) error {
	if s.edges.after != nil {
		return s.edges.after(stage)
	}
	return nil
}
func (s *LockedState) syncFD(fd int, stage string) error {
	if s.edges.sync != nil {
		return s.edges.sync(fd, stage)
	}
	return unix.Fsync(fd)
}
func (s *LockedState) publish(ctx context.Context, name string, raw []byte) error {
	limit := custodySpecBytes // enrollment never inherits the state ceiling
	if name == "state.json" {
		limit = custodyStateLimit(s.binding)
	}
	if len(raw) > limit {
		return ErrCapacity
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	if e := s.ctx.Err(); e != nil {
		return e
	}
	if e := s.identity(); e != nil {
		return e
	}
	var nonce [16]byte
	if _, e := rand.Read(nonce[:]); e != nil {
		return ErrIO
	}
	temp := ".pending-" + hex.EncodeToString(nonce[:])
	fd, e := unix.Openat(int(s.dir.Fd()), temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return ErrIO
	}
	f := os.NewFile(uintptr(fd), "custody-temporary")
	defer f.Close()
	unknown := func() error {
		s.poisoned = true
		if s.grant != nil {
			s.grant.cancel()
		}
		return ErrOutcomeUnknown
	}
	for rest := raw; len(rest) > 0; {
		n, e := f.Write(rest)
		if e != nil || n == 0 {
			return unknown()
		}
		rest = rest[n:]
	}
	if s.after(name+"-write") != nil {
		return unknown()
	}
	if e := s.syncFD(fd, name+"-file-sync"); e != nil {
		return errors.Join(unknown(), custodyDurabilityError(e))
	}
	if s.after(name+"-file-sync") != nil {
		return unknown()
	}
	if f.Close() != nil {
		return unknown()
	}
	if ctx.Err() != nil || s.ctx.Err() != nil {
		return unknown()
	}
	if unix.Renameat(int(s.dir.Fd()), temp, int(s.dir.Fd()), name) != nil {
		return unknown()
	}
	if s.after(name+"-rename") != nil {
		return unknown()
	}
	if e := s.syncFD(int(s.dir.Fd()), name+"-dir-sync"); e != nil {
		return errors.Join(unknown(), custodyDurabilityError(e))
	}
	if s.after(name+"-dir-sync") != nil {
		return unknown()
	}
	check, e := s.read(name, limit)
	if e != nil || !bytes.Equal(raw, check) || s.identity() != nil || ctx.Err() != nil || s.ctx.Err() != nil {
		return unknown()
	}
	return nil
}
func custodyDurabilityError(e error) error {
	if errors.Is(e, unix.EINVAL) || errors.Is(e, unix.ENOTSUP) || errors.Is(e, unix.ENOSYS) {
		return ErrUnsupported
	}
	return ErrIO
}
func (s *LockedState) Authorize(ctx context.Context, a Approval, p Preview, currentInventory Digest) (*Authorization, error) {
	if e := s.enter(ctx, true); e != nil {
		return nil, e
	}
	defer s.mu.Unlock()
	if s.grant != nil {
		return nil, ErrBusy
	}
	snap, e := s.load()
	if e != nil {
		return nil, e
	}
	p, e = NewPreview(p.plan, p.mode, p.base, p.inventory)
	if e != nil {
		return nil, e
	}
	if e = custodyCheckApproval(a, p, snap.Base, currentInventory, s.now()); e != nil {
		return nil, e
	}
	d := a.document
	if snap.Document != nil {
		state := snap.Document
		for _, c := range state.Consumed {
			if c.ID == d.ApprovalID || c.Digest == a.hash {
				return nil, ErrConsumed
			}
		}
		if state.Pending != nil && d.Mode != ModeRecover {
			return nil, ErrConflict
		}
		for _, c := range state.Constructions {
			if !c.Complete && !(d.Mode == ModeRecover && c.ID == d.ConstructionID) {
				return nil, ErrConflict
			}
		}
		if d.ConstructionID != "" {
			c := findConstruction(state, d.ConstructionID)
			if c == nil || c.Input != p.plan.hash || !slices.Equal(c.Namespaces, p.plan.names) {
				return nil, ErrApproval
			}
			if d.Mode == ModeRecover && c.Complete {
				return nil, ErrApproval
			}
		}
		if d.Mode == ModeRecover {
			if d.ConstructionID == "" {
				return nil, ErrApproval
			}
			if state.Pending != nil {
				if e = checkRecovery(state, d.Recovery, d.ConstructionID); e != nil {
					return nil, e
				}
			} else if d.Recovery != nil {
				return nil, ErrApproval
			}
		}
	} else if d.Mode != ModeApply || d.ConstructionID != "" || d.Recovery != nil {
		return nil, ErrApproval
	}
	auth := &Authorization{owner: s, approval: a, preview: p, chain: baseFromWire(baseToWire(snap.Base)), inventory: p.inventory, construction: d.ConstructionID, dispatchable: map[string]bool{}, dispatched: map[string]bool{}}
	auth.self = auth
	return auth, nil
}
func checkRecovery(d *Document, r *Recovery, construction string) error {
	p := d.Pending
	if p == nil || r == nil || p.ConstructionID != construction || r.AttemptID != p.AttemptID || r.OriginalInput != p.Input || r.OriginalPreview != p.Preview || r.OriginalApproval != p.OriginalApproval || r.ResourceKey != p.ResourceKey || r.Operation != p.Operation || r.Inventory == "" {
		return ErrApproval
	}
	original := findResource(d, p.ResourceKey)
	if original == nil || r.AuthoredHash != original.Authored.hash {
		return ErrApproval
	}
	if r.Disposition == DispositionNotApplied {
		// No server ID exists for an independently evidenced never-committed
		// create. Retain its EXACT unassigned marker, never invent an identity.
		// A recorded attributable success contradicts noncommit authority.
		if p.Returned != nil || r.Coordinate != p.Coordinate {
			return ErrApproval
		}
	} else if !coordinateResolves(p.Coordinate, r.Coordinate) {
		return ErrApproval
	}
	if p.Returned != nil && r.Coordinate != p.Returned.Coordinate {
		return ErrApproval
	}
	return nil
}
func (s *LockedState) validAuth(a *Authorization, snap Snapshot) error {
	if a == nil || a.self != a || a.owner != s || a.finished || !custodySameBase(a.chain, snap.Base) {
		return ErrApproval
	}
	return nil
}
func (s *LockedState) live(a *Authorization) bool {
	now := s.now().UnixMilli()
	return now >= a.approval.document.IssuedMS && now < a.approval.document.ExpiresMS
}
func emptyCustody(b Binding) Document {
	return Document{Schema: 1, Binding: b, Reservations: []Reservation{}, Resources: []ResourceRecord{}, Constructions: []Construction{}, Receipts: []Receipt{}, Consumed: []ConsumedApproval{}}
}
func (s *LockedState) Bootstrap(ctx context.Context, a *Authorization) (Snapshot, error) {
	if e := s.enter(ctx, true); e != nil {
		return Snapshot{}, e
	}
	defer s.mu.Unlock()
	if s.grant != nil {
		return Snapshot{}, ErrBusy
	}
	snap, e := s.load()
	if e != nil {
		return Snapshot{}, e
	}
	if !snap.Base.Pristine || s.validAuth(a, snap) != nil || a.preview.mode != ModeApply || !s.live(a) {
		return Snapshot{}, ErrApproval
	}
	d := emptyCustody(s.binding)
	id := a.approval.document.ApprovalID // first construction has stable approval identity
	if e = startCustody(&d, a, id); e != nil {
		return Snapshot{}, e
	}
	d.Generation = 1
	raw, e := custodyDocumentBytes(d)
	if e != nil {
		return Snapshot{}, e
	}
	next, e := custodyDocumentDecode(raw)
	if e != nil {
		return Snapshot{}, e
	}
	if e = custodyBudget(next, id); e != nil {
		return Snapshot{}, e
	}
	if e = custodyTeardownForecast(next, id); e != nil {
		return Snapshot{}, e
	}
	ew := enrollmentWire{Schema: 1, Binding: bindingToWire(s.binding)}
	pre, _ := json.Marshal(ew)
	ew.Checksum = custodyHash("enrollment", pre)
	marker, _ := json.Marshal(ew)
	if e = s.publish(ctx, "enrollment.json", marker); e != nil {
		return Snapshot{}, e
	}
	if e = s.publish(ctx, "state.json", raw); e != nil {
		s.poisoned = true
		return Snapshot{}, errors.Join(ErrOutcomeUnknown, e)
	}
	a.construction = id
	a.started = true
	out := snapshotOf(next)
	a.chain = baseFromWire(baseToWire(out.Base))
	return out, nil
}
func snapshotOf(d Document) Snapshot {
	h := Head{d.Generation, d.Checksum}
	return Snapshot{Base{Head: &h}, &d}
}
func findConstruction(d *Document, id string) *Construction {
	for i := range d.Constructions {
		if d.Constructions[i].ID == id {
			return &d.Constructions[i]
		}
	}
	return nil
}
func findResource(d *Document, key string) *ResourceRecord {
	for i := range d.Resources {
		if d.Resources[i].Key == key {
			return &d.Resources[i]
		}
	}
	return nil
}
func planResource(p PlanInput, key string) *PlannedResource {
	for i := range p.Resources {
		if p.Resources[i].Key == key {
			return &p.Resources[i]
		}
	}
	return nil
}
func consumeCustody(d *Document, a *Authorization, id string) error {
	for _, c := range d.Consumed {
		if c.ID == a.approval.document.ApprovalID || c.Digest == a.approval.hash {
			return ErrConsumed
		}
	}
	d.Consumed = append(d.Consumed, ConsumedApproval{a.approval.document.ApprovalID, a.approval.hash, id, baseFromWire(baseToWire(a.approval.document.Base))})
	return nil
}
func invalidateCustody(d *Document, names []string, id string) {
	for i := range d.Receipts {
		r := &d.Receipts[i]
		if r.Mode == ModeApply && r.InvalidatedBy == "" && overlapCustody(r.Namespaces, names) {
			r.InvalidatedBy = id
		}
	}
}
func overlapCustody(a, b []string) bool {
	for _, s := range a {
		if slices.Contains(b, s) {
			return true
		}
	}
	return false
}
func currentCustodyReceipt(d *Document, c *Construction) bool {
	if d.Pending != nil || !c.Complete {
		return false
	}
	for _, other := range d.Constructions {
		if !other.Complete && overlapCustody(other.Namespaces, c.Namespaces) {
			return false
		}
	}
	for i := len(d.Receipts) - 1; i >= 0; i-- {
		r := d.Receipts[i]
		if r.ConstructionID != c.ID || r.Mode != ModeApply || r.Result != ReceiptApplyObserved {
			continue
		}
		if r.InvalidatedBy != "" || r.Input != c.Input || !slices.Equal(r.Namespaces, c.Namespaces) {
			return false
		}
		for _, m := range r.Members {
			actual := findResource(d, m.ResourceKey)
			if actual == nil || m.Outcome != OutcomePresent || m.Observation == nil || actual.Status != ResourceOwned || actual.Coordinate != m.Coordinate || actual.ManagedHash != m.Observation.ManagedHash || actual.ServerRevision != m.Observation.ServerRevision {
				return false
			}
		}
		return true
	}
	return false
}
func startCustody(d *Document, a *Authorization, id string) error {
	if !custodyText(id, false) || a.started || d.Pending != nil {
		return ErrConflict
	}
	p := a.preview.plan
	if existing := findConstruction(d, id); existing != nil {
		if a.construction != id || existing.Input != p.hash {
			return ErrApproval
		}
		if a.preview.mode == ModeApply {
			if existing.Mode != ModeApply || !currentCustodyReceipt(d, existing) {
				return ErrConflict
			}
		} else if a.preview.mode != ModeRecover || existing.Complete {
			return ErrConflict
		}
		return consumeCustody(d, a, id)
	}
	if a.construction != "" || a.preview.mode == ModeRecover {
		return ErrApproval
	}
	if p.input.Mode == ModeApply {
		for _, claim := range p.claims {
			for _, old := range d.Reservations {
				if old.Namespace == claim.Namespace {
					return ErrConflict
				}
			}
		}
		for _, r := range p.input.Resources {
			if findResource(d, r.Key) != nil {
				return ErrConflict
			}
			id := custodyIdentity(r.Coordinate)
			if id != "" {
				for _, old := range d.Resources {
					if custodyIdentity(old.Coordinate) == id {
						return ErrConflict
					}
				}
			}
		}
		for _, claim := range p.claims {
			claim.FirstConstruction = id
			claim.Approval = a.approval.hash
			d.Reservations = append(d.Reservations, claim)
		}
		for _, r := range p.input.Resources {
			d.Resources = append(d.Resources, ResourceRecord{Key: r.Key, Origin: r.Origin, Coordinate: r.Coordinate, Authored: r.Spec, Dependencies: append([]string{}, r.Dependencies...), Namespaces: append([]string{}, r.Namespaces...), Acquisition: []Evidence{}, Observations: []Observation{}, Status: ResourceReserved})
		}
	} else {
		for _, cl := range p.claims {
			found := false
			for _, old := range d.Reservations {
				if old.Namespace == cl.Namespace && old.WireName == cl.WireName && old.Origin == cl.Origin {
					found = true
				}
			}
			if !found {
				return ErrConflict
			}
		}
		for _, r := range p.input.Resources {
			old := findResource(d, r.Key)
			if old == nil || old.Status == ResourceReserved || old.Status == ResourceNotApplied || len(old.Acquisition) == 0 || old.Origin != r.Origin || old.Coordinate != r.Coordinate || old.Authored.hash != r.Spec.hash || !slices.Equal(old.Dependencies, r.Dependencies) || !slices.Equal(old.Namespaces, r.Namespaces) {
				return ErrConflict
			}
			if old.Status == ResourceRemoved {
				for _, step := range p.input.Steps {
					if step.ResourceKey == old.Key {
						return ErrConflict
					}
				}
			}
		}
		invalidateCustody(d, p.names, id)
	}
	c := Construction{ID: id, Input: p.hash, Preview: a.preview.hash, Approval: a.approval.hash, Mode: p.input.Mode, Base: baseFromWire(baseToWire(a.chain)), Namespaces: append([]string{}, p.names...), Plan: p.input, Steps: []StepRecord{}, Classifications: []CleanupClassification{}}
	for _, step := range c.Plan.Steps {
		r := planResource(c.Plan, step.ResourceKey)
		c.Steps = append(c.Steps, StepRecord{Number: step.Number, Status: StepPlanned, Coordinate: r.Coordinate, Evidence: []Evidence{}})
	}
	d.Constructions = append(d.Constructions, c)
	return consumeCustody(d, a, id)
}

// Admission sizes the actual private wire at every remaining protocol stage.
// This is a size-only upper envelope, not fabricated custody: it is never saved
// or decoded as evidence. HTML escaping of a legal '<' occupies six bytes; use
// the serializer itself for strings, repeated coordinates, lists and objects.
// Materialized stages replace their reservation rather than adding it again.
func custodyBudget(d Document, construction string) error {
	limit := custodyStateLimit(d.Binding)
	if limit == 0 {
		return ErrInvalid
	}
	if len(d.Resources) > custodyMaxResources || len(d.Reservations) > custodyMaxClaims {
		return ErrCapacity
	}
	w := documentToWire(d)
	original := findConstruction(&d, construction)
	var c *constructionWire
	for i := range w.Constructions {
		if w.Constructions[i].ID == construction {
			c = &w.Constructions[i]
		}
	}
	if c == nil || original == nil || len(c.Steps) > custodyMaxSteps {
		return ErrCorrupt
	}
	// Reserve generation space as well as bytes before granting any dispatch.
	// A response followed by recovery and a fresh suffix Start is the longest
	// remaining path for one attempt; repeated local passes recheck admission.
	commits := uint64(1) // Finish
	for _, step := range c.Steps {
		switch step.Status {
		case StepPlanned:
			commits += 4
		case StepIntent:
			commits += 3
		case StepResponse:
			commits += 2
		}
	}
	if c.Mode != ModeApply {
		commits += uint64(len(c.Plan.Resources))
	}
	if d.Generation > math.MaxUint64-commits {
		return ErrCapacity
	}
	text := strings.Repeat("<", 256)
	hash := Digest(strings.Repeat("f", 64))
	maxHead := headWire{math.MaxUint64, hash}
	w.Generation = math.MaxUint64
	es := make([]evidenceWire, 16)
	for i := range es {
		es[i] = evidenceWire{text, hash}
	}
	// Authored canonical JSON is already bounded by its encoded bytes.
	desired := json.RawMessage(`{"x":"` + strings.Repeat("a", custodySpecBytes-8) + `"}`)
	resources := map[string]*resourceWire{}
	for i := range w.Resources {
		resources[w.Resources[i].Key] = &w.Resources[i]
	}
	alternativeCredit := 0
	// size is always exactly len(json.Marshal(w)). The whole envelope is
	// encoded once; afterwards only the touched components (this step, its
	// resource without observations, pending, appended array elements) are
	// re-encoded and the length difference is applied. Each component is
	// encoded by json.Marshal itself, never sized from len(raw) of its parts.
	size := 0
	check := func() error {
		if size-alternativeCredit > limit {
			return ErrCapacity
		}
		return nil
	}
	observation := func(coordinate coordinateWire) observationWire {
		return observationWire{coordinate, PresencePresent, hash, hash, hash, text, es}
	}
	resolved := func(coordinate coordinateWire) coordinateWire {
		if coordinate.Unassigned {
			coordinate.Unassigned = false
			coordinate.ID = text
			if coordinate.Kind == KindDraft {
				coordinate.Version = text
			}
		}
		return coordinate
	}
	// Future intents may irreversibly invalidate an existing receipt. The
	// invalidation identifier is independently bounded, not the original ID.
	for i := range w.Receipts {
		if w.Receipts[i].InvalidatedBy == "" && overlapCustody(w.Receipts[i].Namespaces, c.Namespaces) {
			w.Receipts[i].InvalidatedBy = text
		}
	}
	// A cleanup classification is an alternative to never-attempted mutations.
	// Reserve its metadata too; share the already-reserved observation/step
	// slots with the mutation path. Repeated local passes require new admission.
	classAlternatives := map[string]int{}
	if c.Mode != ModeApply {
		latestClass := map[string]CleanupReason{}
		for _, cl := range original.Classifications {
			latestClass[cl.ResourceKey] = cl.Reason
		}
		for _, pr := range c.Plan.Resources {
			eligible, remaining := true, 0
			for i, step := range c.Steps {
				if c.Plan.Steps[i].ResourceKey != pr.Key {
					continue
				}
				if step.Status != StepPlanned && step.Status != StepClassified {
					eligible = false
				}
				if step.Status == StepPlanned {
					remaining++
				}
			}
			if !eligible {
				continue
			}
			// Only the latest settled classification consumes this future slot.
			// All steps for it must already be classified (or it has no steps).
			// Unresolved still needs settlement; planned mutations still need
			// their alternative. A repeat local classification is admitted anew
			// by Commit before publication, not promised by an earlier grant.
			if reason := latestClass[pr.Key]; remaining == 0 && reason != "" && reason != CleanupUnresolved {
				continue
			}
			r := resources[pr.Key]
			o := observation(r.Coordinate)
			c.Classifications = append(c.Classifications, classificationWire{pr.Key, r.Coordinate, maxHead, CleanupRetainedUnsupported, &o, hash, es})
			if remaining == 0 {
				r.Observations = append(r.Observations, o)
			}
			if remaining == 2 {
				classAlternatives[pr.Key] = len(r.Observations)
			}
		}
	}
	full, err := json.Marshal(w)
	if err != nil {
		return ErrCorrupt
	}
	size = len(full)
	if err := check(); err != nil {
		return err
	}
	// Every value below was part of the encoded envelope or is built from
	// fixed bounded values, so its own encoding cannot fail; ErrCorrupt is
	// kept only as the conservative answer the whole-document encode gave.
	pendingLen, err := custodyPendingLen(w.Pending)
	if err != nil {
		return ErrCorrupt
	}
	consumedLen := -1 // the same bounded record every time; memoised per call
	for i := range c.Steps {
		step := &c.Steps[i]
		if step.Status != StepPlanned && step.Status != StepIntent && step.Status != StepResponse {
			continue
		}
		ps := c.Plan.Steps[i]
		pr := planResource(original.Plan, ps.ResourceKey)
		r := resources[ps.ResourceKey]
		if pr == nil || r == nil {
			return ErrCorrupt
		}
		stepLen, err1 := custodyWireLen(*step)
		resourceLen, err2 := custodyResourceLen(r)
		if err1 != nil || err2 != nil {
			return ErrCorrupt
		}
		// remeasure applies the encoded-length change of everything this step
		// may have touched since the last checkpoint.
		remeasure := func() error {
			s, err1 := custodyWireLen(*step)
			rl, err2 := custodyResourceLen(r)
			pl, err3 := custodyPendingLen(w.Pending)
			if err1 != nil || err2 != nil || err3 != nil {
				return ErrCorrupt
			}
			size += s - stepLen + rl - resourceLen + pl - pendingLen
			stepLen, resourceLen, pendingLen = s, rl, pl
			return nil
		}
		// Allow independent recovery attribution plus a fresh suffix approval.
		// Their documents are not stored, only these bounded consumed records.
		for range 2 {
			record := consumedWire{text, hash, c.ID, baseWire{Head: &maxHead}}
			if consumedLen < 0 {
				if consumedLen, err = custodyWireLen(record); err != nil {
					return ErrCorrupt
				}
			}
			size += custodyAppendLen(w.Consumed, consumedLen)
			w.Consumed = append(w.Consumed, record)
		}
		if step.Status == StepPlanned {
			coordinate := coordinateWire(pr.Coordinate)
			if pr.VersionFrom != "" {
				coordinate.Version = resources[pr.VersionFrom].Coordinate.Version
			}
			step.AttemptID, step.Status, step.Coordinate = text, StepIntent, coordinate
			if r.FirstAttempt == "" {
				r.FirstAttempt = text
			}
			w.Pending = &pendingWire{step.AttemptID, c.ID, pr.Key, hash, c.Input, hash, hash, maxHead, c.Namespaces, coordinate, ps.Operation, custodyMethod(coordinate.Kind, ps.Operation), desired, hash, hash, StepIntent, nil}
			if err := remeasure(); err != nil {
				return err
			}
			if err := check(); err != nil {
				return err
			}
		}
		if w.Pending == nil || w.Pending.ResourceKey != pr.Key {
			return ErrCorrupt
		}
		p := w.Pending
		if step.Status == StepIntent {
			coordinate := resolved(p.Coordinate)
			p.Returned = &responseWire{coordinate, es, &hash}
			p.Phase, step.Status, step.Coordinate = StepResponse, StepResponse, coordinate
			if p.Coordinate.Unassigned {
				r.Coordinate = coordinate
			}
			if err := remeasure(); err != nil {
				return err
			}
			if err := check(); err != nil {
				return err
			}
		}
		o := observation(p.Returned.Coordinate)
		if ps.Operation == OpDelete {
			o.Presence, o.ManagedHash, o.ServerRevision = PresenceAbsent, "", ""
		} else {
			r.ManagedHash, r.ServerRevision = hash, text
		}
		if len(r.Acquisition) == 0 {
			r.Acquisition = es
		}
		r.Coordinate = o.Coordinate
		r.Status = ResourceNotApplied // longest status, including recovery outcome
		observationLen, err := custodyWireLen(o)
		if err != nil {
			return ErrCorrupt
		}
		size += custodyAppendLen(r.Observations, observationLen)
		r.Observations = append(r.Observations, o)
		step.Status, step.Coordinate, step.Evidence = StepNotApplied, o.Coordinate, es
		w.Pending = nil
		if err := remeasure(); err != nil {
			return err
		}
		if err := check(); err != nil {
			return err
		}
	}
	// Cleanup has at most disable+delete per resource. The classification
	// alternative appends ONE observation, not both mutation observations. The
	// larger of those two bounds its observation; its metadata contains another
	// full observation plus evidence, so also covers the smaller mutation slot.
	// Deduct only that duplicate virtual slot at the final-stage comparison.
	// No persisted history or actual incoming result is removed or discounted.
	for key, first := range classAlternatives {
		obs := resources[key].Observations
		one, err1 := json.Marshal(obs[first])
		two, err2 := json.Marshal(obs[first+1])
		if err1 != nil || err2 != nil {
			return ErrCorrupt
		}
		alternativeCredit += min(len(one), len(two)) + 1
	}
	// A receipt has independent member/read evidence. Never count it as a
	// small identifier-only checkpoint or retain an extra pending reservation.
	members := []memberWire{}
	for _, pr := range c.Plan.Resources {
		r := resources[pr.Key]
		o := observation(r.Coordinate)
		members = append(members, memberWire{r.Key, r.Coordinate, OutcomeRetainedUnsupported, &o, es})
	}
	receipt := receiptWire{text, c.ID, c.Mode, ReceiptApplyObserved, hash, c.Input, hash, hash, c.Namespaces, members, hash, math.MaxInt64, text}
	receiptLen, err := custodyWireLen(receipt)
	if err != nil {
		return ErrCorrupt
	}
	size += custodyAppendLen(w.Receipts, receiptLen)
	w.Receipts = append(w.Receipts, receipt)
	return check()
}

// custodyWireLen is the exact encoded length json.Marshal gives v, which is
// also its length as a field or array element of the document: the encoder
// is context-free (compact, HTML-escaped RawMessage included).
func custodyWireLen(v any) (int, error) {
	raw, err := json.Marshal(v)
	return len(raw), err
}

// custodyAppendLen is the growth of an encoded array when one element of
// encoded length n is appended: null becomes [x], [] becomes [x], and a
// non-empty array gains a comma.
func custodyAppendLen[T any](s []T, n int) int {
	switch {
	case s == nil:
		return n + 2 - len("null")
	case len(s) == 0:
		return n
	}
	return n + 1
}

// custodyPendingLen encodes the pending slot, null when empty.
func custodyPendingLen(p *pendingWire) (int, error) {
	if p == nil {
		return len("null"), nil
	}
	return custodyWireLen(*p)
}

// custodyResourceLen encodes a resource without its observation list, which
// only grows by appends that are sized separately. Swapping the list for
// null changes the encoded length by a constant for that list, so the
// differences between two measurements are exact.
func custodyResourceLen(r *resourceWire) (int, error) {
	v := *r
	v.Observations = nil
	return custodyWireLen(v)
}

// custodyTeardownForecast refuses an apply, no-op apply or deactivate
// construction (and a suffix recovery of one) whose worst-case end document
// would leave the canonical full teardown refused by custodyBudget. Under
// create-only provisioning that would strand permanent objects. It runs once
// per Start, on the post-Start document, after custodyBudget admitted the
// construction itself; custodyBudget is reused unchanged as the teardown judge.
//
// The end document is a size-only upper envelope, never saved or decoded as
// evidence. Every value the backend or a later approval supplies is at its
// encoded maximum: custodyText 256 bytes of '<' (six escaped bytes each),
// 16 evidence entries, 64-hex digests, the longest status/outcome/result.
// Authored specs, plans and settled history are the real persisted values.
//
// Recovery reserve: each unsettled step reserves one recovery, the resolve
// approval plus the fresh suffix Start approval (two consumed records, as
// custodyBudget reserves per remaining step). A construction is the only
// incomplete one at its Start (Authorize, single-incomplete-construction
// rule), so only it needs simulating. ChangeResolve, which settles pending,
// is never checked here; a suffix recovery Start is, which bounds repeated
// no-progress recovery Starts by the slack the actual history leaves.
//
// A planned no-op re-apply is itself a construction Start and is checked
// against its own end document (its receipt included), so no no-op count is
// assumed: the last admitted no-op still leaves the teardown admitted.
func custodyTeardownForecast(d Document, construction string) error {
	c := findConstruction(&d, construction)
	if c == nil {
		return ErrCorrupt
	}
	if c.Mode != ModeApply && c.Mode != ModeDeactivate {
		return nil // a teardown Start is custodyBudget's own admission
	}
	if d.Binding.ContractRevision != custodyStateV2Revision {
		// Only the engine contract can dispatch a mutation (prepareEngine,
		// engineClient, DecodeBinding all require it); a legacy-bound state
		// keeps its original admission unchanged, like its original ceiling.
		return nil
	}
	// Exact decision either way: the bound dominates every custodyBudget
	// checkpoint, so a bound inside the limit is an admission custodyBudget
	// would also give. Anything else is decided by custodyBudget itself.
	if bound, e := custodyForecastBound(d, construction); e == nil && bound <= custodyStateLimit(d.Binding) {
		return nil
	}
	t, id, e := custodyForecastDocument(d, construction, custodyForecastValues(false))
	if e != nil {
		return e
	}
	if e := custodyBudget(t, id); e != nil {
		if errors.Is(e, ErrCapacity) {
			return e
		}
		return errors.Join(ErrCapacity, e)
	}
	return nil
}

// custodyForecastMax holds the size-only maximum values every forecast uses.
// Free text (T) and evidence lists (E) are placed through methods so that a
// counting envelope can place one-byte stand-ins and add each placement's
// exact encoded difference afterwards (the encoder is context-free). The
// identifier text (id) used for coordinates and construction identities is
// always placed in full.
type custodyForecastMax struct {
	text, id     string
	hash         Digest
	base         Base
	es           []Evidence
	texts, lists *int
}

func custodyForecastValues(counting bool) custodyForecastMax {
	m := custodyForecastMax{text: strings.Repeat("<", 256), id: strings.Repeat("<", 255) + ">", hash: Digest(strings.Repeat("f", 64))}
	m.base = Base{Head: &Head{math.MaxUint64, m.hash}}
	m.es = custodyForecastEvidence(m.text, m.hash)
	if counting {
		m.text, m.es, m.texts, m.lists = "x", []Evidence{{}}, new(int), new(int)
	}
	return m
}
func custodyForecastEvidence(text string, hash Digest) []Evidence {
	es := make([]Evidence, 16)
	for i := range es {
		es[i] = Evidence{text, hash}
	}
	return es
}
func (m custodyForecastMax) T() string {
	if m.texts != nil {
		*m.texts++
	}
	return m.text
}
func (m custodyForecastMax) E() []Evidence {
	if m.lists != nil {
		*m.lists++
	}
	return m.es
}

// placed is the encoded growth of the counted placements over their stand-ins.
func (m custodyForecastMax) placed(texts, lists int) (int, error) {
	if m.texts == nil {
		return 0, nil
	}
	full := custodyForecastValues(false)
	a, e1 := custodyWireLen(full.text)
	b, e2 := custodyWireLen(m.text)
	x, e3 := custodyWireLen(evidenceToWire(full.es))
	y, e4 := custodyWireLen(evidenceToWire(m.es))
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
		return 0, ErrCorrupt
	}
	return texts*(a-b) + lists*(x-y), nil
}
func (m custodyForecastMax) observation(co Coordinate) Observation {
	return Observation{co, PresencePresent, m.hash, m.hash, m.hash, m.T(), m.E()}
}

// custodyForecastSettle takes construction c of d to its worst-case end: every
// unsettled step at its longest settled form with one reserved recovery (two
// consumed records), a classification per member for a cleanup mode, every
// unmarked receipt invalidated, and its receipt. It never aliases the caller's
// lists: every list it changes is copied first. A real end document (mirror
// false) gives every cleanup member a classification observation too; mirror
// true follows custodyBudget's envelope, which shares that observation slot
// with a member's mutation steps and appends it only to a step-less member.
// It returns the counted placements inside the receipt it appends.
func custodyForecastSettle(d *Document, construction string, m custodyForecastMax, mirror bool) (int, int, error) {
	d.Resources = slices.Clone(d.Resources)
	d.Constructions = slices.Clone(d.Constructions)
	d.Receipts = slices.Clone(d.Receipts)
	d.Consumed = slices.Clip(d.Consumed)
	d.Pending = nil
	c := findConstruction(d, construction)
	if c == nil {
		return 0, 0, ErrCorrupt
	}
	c.Steps = slices.Clone(c.Steps)
	c.Classifications = slices.Clip(c.Classifications)
	stepped := map[string]bool{}
	for i, step := range c.Steps {
		if step.Status == StepPlanned && i < len(c.Plan.Steps) {
			stepped[c.Plan.Steps[i].ResourceKey] = true
		}
	}
	worst := func(r *ResourceRecord, pr *PlannedResource) Coordinate {
		co := r.Coordinate
		if co.Unassigned {
			co.Unassigned, co.ID = false, m.id
			if co.Kind == KindDraft {
				co.Version = m.id
			}
		}
		if pr != nil && pr.VersionFrom != "" {
			co.Version = m.id
		}
		return co
	}
	for i := range c.Steps {
		step := &c.Steps[i]
		if step.Status != StepPlanned && step.Status != StepIntent && step.Status != StepResponse {
			continue
		}
		if i >= len(c.Plan.Steps) {
			return 0, 0, ErrCorrupt
		}
		ps := c.Plan.Steps[i]
		r := findResource(d, ps.ResourceKey)
		if r == nil {
			return 0, 0, ErrCorrupt
		}
		// One recovery per unsettled step: its resolve and its fresh suffix Start.
		d.Consumed = append(d.Consumed, ConsumedApproval{m.T(), m.hash, c.ID, m.base}, ConsumedApproval{m.T(), m.hash, c.ID, m.base})
		co := worst(r, planResource(c.Plan, ps.ResourceKey))
		if r.FirstAttempt == "" {
			r.FirstAttempt = m.T()
		}
		if len(r.Acquisition) == 0 {
			r.Acquisition = m.E()
		}
		r.Coordinate, r.ManagedHash, r.ServerRevision, r.Status = co, m.hash, m.T(), ResourceNotApplied
		r.Observations = append(slices.Clip(r.Observations), m.observation(co))
		step.AttemptID, step.Status, step.Coordinate, step.Evidence = m.T(), StepNotApplied, co, m.E()
	}
	if c.Mode != ModeApply {
		for _, pr := range c.Plan.Resources {
			r := findResource(d, pr.Key)
			if r == nil {
				return 0, 0, ErrCorrupt
			}
			// A cleanup may classify each member instead of mutating it.
			o := m.observation(r.Coordinate)
			c.Classifications = append(c.Classifications, CleanupClassification{pr.Key, r.Coordinate, *m.base.Head, CleanupRetainedUnsupported, &o, m.hash, m.E()})
			if !mirror || !stepped[pr.Key] {
				r.Observations = append(slices.Clip(r.Observations), m.observation(r.Coordinate))
			}
		}
	}
	for i := range d.Receipts {
		if d.Receipts[i].InvalidatedBy == "" {
			d.Receipts[i].InvalidatedBy = m.T()
		}
	}
	texts, lists := 0, 0
	if m.texts != nil {
		texts, lists = *m.texts, *m.lists
	}
	members := []ReceiptMember{}
	for _, pr := range c.Plan.Resources {
		r := findResource(d, pr.Key)
		if r == nil {
			return 0, 0, ErrCorrupt
		}
		o := m.observation(r.Coordinate)
		members = append(members, ReceiptMember{pr.Key, r.Coordinate, OutcomeRetainedUnsupported, &o, m.E()})
	}
	d.Receipts = append(d.Receipts, Receipt{m.T(), c.ID, c.Mode, ReceiptApplyObserved, m.hash, c.Input, m.hash, m.hash, c.Namespaces, members, m.hash, math.MaxInt64, m.T()})
	if m.texts != nil {
		texts, lists = *m.texts-texts, *m.lists-lists
	}
	return texts, lists, nil
}

// custodyForecastDocument is d after construction's worst-case end with the
// canonical full teardown Started on it: every acquired resource, consumers
// disabled then every deletable kind deleted; a removed resource stays a
// step-less member (the historical consumer closure must name it).
func custodyForecastDocument(d Document, construction string, m custodyForecastMax) (Document, string, error) {
	c := findConstruction(&d, construction)
	if c == nil {
		return Document{}, "", ErrCorrupt
	}
	// Carry custodyBudget's remaining-generation reserve forward before
	// reserving the teardown itself. The input is already post-Start; its
	// Finish, per-step recovery/suffix reserve and cleanup classifications
	// still cost commits, as does the future teardown Start.
	commits := uint64(2) // current Finish + future teardown Start
	for _, step := range c.Steps {
		switch step.Status {
		case StepPlanned:
			commits += 4
		case StepIntent:
			commits += 3
		case StepResponse:
			commits += 2
		}
	}
	if c.Mode != ModeApply {
		commits += uint64(len(c.Plan.Resources))
	}
	if d.Generation > math.MaxUint64-commits {
		return Document{}, "", ErrCapacity
	}
	d.Generation += commits
	if _, _, e := custodyForecastSettle(&d, construction, m, false); e != nil {
		return Document{}, "", e
	}
	declared := map[Origin]int{}
	tp := PlanInput{Binding: d.Binding, Mode: ModeTeardown, Declarations: []DeclarationInput{}, Targets: []Origin{}, Resources: []PlannedResource{}, Steps: []PlannedStep{}}
	names := []string{}
	for _, old := range d.Constructions {
		for _, n := range old.Namespaces {
			if !slices.Contains(names, n) {
				names = append(names, n)
			}
		}
		if old.Mode != ModeApply {
			continue
		}
		for _, o := range old.Plan.Targets {
			if !slices.Contains(tp.Targets, o) {
				tp.Targets = append(tp.Targets, o)
			}
		}
		for _, x := range old.Plan.Declarations {
			i, ok := declared[x.Origin]
			if !ok {
				declared[x.Origin] = len(tp.Declarations)
				tp.Declarations = append(tp.Declarations, x)
				continue
			}
			a, e1 := json.Marshal(planToWire(PlanInput{Declarations: []DeclarationInput{tp.Declarations[i]}}))
			b, e2 := json.Marshal(planToWire(PlanInput{Declarations: []DeclarationInput{x}}))
			if e1 != nil || e2 != nil {
				return Document{}, "", ErrCorrupt
			}
			if len(b) > len(a) {
				tp.Declarations[i] = x
			}
		}
	}
	disables, deletes := []string{}, []string{}
	for _, r := range d.Resources {
		if len(r.Acquisition) == 0 {
			continue
		}
		tp.Resources = append(tp.Resources, PlannedResource{Key: r.Key, Origin: r.Origin, Coordinate: r.Coordinate, Spec: r.Authored, Dependencies: r.Dependencies, Namespaces: r.Namespaces})
		for _, n := range r.Namespaces {
			if !slices.Contains(names, n) {
				names = append(names, n)
			}
		}
		if r.Status == ResourceRemoved {
			continue
		}
		if isConsumer(r.Coordinate.Kind) {
			disables = append(disables, r.Key)
		}
		if custodyMethod(r.Coordinate.Kind, OpDelete) != "" {
			deletes = append(deletes, r.Key)
		}
	}
	slices.Sort(names)
	for _, key := range disables {
		tp.Steps = append(tp.Steps, PlannedStep{uint32(len(tp.Steps) + 1), key, OpDisable})
	}
	for _, key := range deletes {
		tp.Steps = append(tp.Steps, PlannedStep{uint32(len(tp.Steps) + 1), key, OpDelete})
	}
	if len(tp.Steps) > custodyMaxSteps || len(tp.Resources) > custodyMaxResources {
		return Document{}, "", ErrCapacity
	}
	// The teardown's identifiers are also at the custodyText maximum, and
	// distinct from every recorded construction and approval identifier.
	id := custodyForecastID(&d, m.id)
	if id == "" {
		return Document{}, "", ErrCapacity
	}
	t := Construction{ID: id, Input: m.hash, Preview: m.hash, Approval: m.hash, Mode: ModeTeardown, Base: m.base, Namespaces: names, Plan: tp, Steps: []StepRecord{}, Classifications: []CleanupClassification{}}
	for _, step := range tp.Steps {
		r := planResource(tp, step.ResourceKey)
		t.Steps = append(t.Steps, StepRecord{Number: step.Number, Status: StepPlanned, Coordinate: r.Coordinate, Evidence: []Evidence{}})
	}
	d.Constructions = append(d.Constructions, t)
	d.Consumed = append(d.Consumed, ConsumedApproval{id, m.hash, id, m.base})
	return d, id, nil
}

// custodyForecastBound is an upper bound on every size custodyBudget compares
// for the forecast teardown of d (custodyForecastDocument), from one encode
// of a counting envelope instead of custodyBudget's per-step re-encodes: the
// teardown settled by custodyForecastSettle (mirror) dominates every
// checkpoint field by field (each settled step, resource, consumed reserve,
// classification, observation and the receipt is at least the envelope
// custodyBudget builds), plus the largest pending slot any step can hold, less
// custodyBudget's own final credit. The encoder is context-free: a stand-in
// changes a length by exactly its encoded difference.
func custodyForecastBound(d Document, construction string) (int, error) {
	m := custodyForecastValues(true)
	d, id, e := custodyForecastDocument(d, construction, m)
	if e != nil {
		return 0, e
	}
	full := custodyForecastValues(false)
	c := findConstruction(&d, id)
	if c == nil || len(c.Steps) > custodyMaxSteps || len(d.Resources) > custodyMaxResources || len(d.Reservations) > custodyMaxClaims {
		return 0, ErrCorrupt
	}
	if commits := uint64(1+4*len(c.Steps)) + uint64(len(c.Plan.Resources)); d.Generation > math.MaxUint64-commits {
		return 0, ErrCapacity
	}
	desired, err := json.Marshal(json.RawMessage(`{"x":"` + strings.Repeat("a", custodySpecBytes-8) + `"}`))
	if err != nil {
		return 0, ErrCorrupt
	}
	// The largest pending: each varying field (key, coordinate, operation) at
	// its longest over the steps, the method at the longest any step has.
	key, op, keyLen, coLen, opLen := "", Operation(""), -1, -1, -1
	var co coordinateWire
	for i := range c.Steps {
		ps := c.Plan.Steps[i]
		pr := planResource(c.Plan, ps.ResourceKey)
		if pr == nil {
			return 0, ErrCorrupt
		}
		x := coordinateWire(pr.Coordinate)
		if x.Unassigned {
			x.Unassigned, x.ID = false, full.id
		}
		x.Version = full.id // never shorter than any resolved version
		a, e1 := custodyWireLen(pr.Key)
		b, e2 := custodyWireLen(x)
		o, e3 := custodyWireLen(ps.Operation)
		if e1 != nil || e2 != nil || e3 != nil {
			return 0, ErrCorrupt
		}
		if a > keyLen {
			key, keyLen = pr.Key, a
		}
		if b > coLen {
			co, coLen = x, b
		}
		if o > opLen {
			op, opLen = ps.Operation, o
		}
	}
	pending := 0
	if len(c.Steps) > 0 {
		maxHead := headWire{math.MaxUint64, full.hash}
		cw := documentToWire(Document{Constructions: []Construction{{Namespaces: c.Namespaces}}}).Constructions[0]
		p := pendingWire{full.text, c.ID, key, full.hash, c.Input, full.hash, full.hash, maxHead, cw.Namespaces, co, op, "DELETE", json.RawMessage(`{}`), full.hash, full.hash, StepResponse, &responseWire{co, evidenceToWire(full.es), &full.hash}}
		n, err := custodyWireLen(p)
		if err != nil {
			return 0, ErrCorrupt
		}
		pending = n - len(`{}`) + len(desired)
	}
	// custodyBudget's only discount: at its final comparison, each member with
	// exactly a disable and a delete step credits the smaller of the two
	// observations it appended (sized here from the unresolved plan coordinate,
	// never larger than custodyBudget's own).
	credit := 0
	for _, pr := range c.Plan.Resources {
		n := 0
		for i, step := range c.Steps {
			if c.Plan.Steps[i].ResourceKey == pr.Key && step.Status == StepPlanned {
				n++
			}
		}
		if n != 2 {
			continue
		}
		present := observationToWire(Observation{pr.Coordinate, PresencePresent, full.hash, full.hash, full.hash, full.text, full.es})
		absent := present
		absent.Presence, absent.ManagedHash, absent.ServerRevision = PresenceAbsent, "", ""
		one, err1 := custodyWireLen(present)
		two, err2 := custodyWireLen(absent)
		if err1 != nil || err2 != nil {
			return 0, ErrCorrupt
		}
		credit += min(one, two) + 1
	}
	before := slices.Clone(d.Receipts)
	rt, rl, e := custodyForecastSettle(&d, id, m, true)
	if e != nil {
		return 0, e
	}
	d.Generation = math.MaxUint64
	short, err := custodyWireLen(documentToWire(d))
	shortReceipt, err2 := custodyWireLen(documentToWire(Document{Receipts: d.Receipts[len(d.Receipts)-1:]}).Receipts[0])
	if err != nil || err2 != nil {
		return 0, ErrCorrupt
	}
	growth, e1 := m.placed(*m.texts, *m.lists)
	receiptGrowth, e2 := m.placed(rt, rl)
	if e1 != nil || e2 != nil {
		return 0, ErrCorrupt
	}
	total := short + growth
	receipt := shortReceipt + receiptGrowth
	// Every comparison before the receipt holds at most the settled envelope
	// without it plus one pending slot (encoded null when empty); the final one
	// holds the whole settled envelope less the credit.
	withoutReceipt := total - custodyAppendLen(before, receipt)
	return max(withoutReceipt-len("null")+pending, total-credit), nil
}

// custodyForecastID is an unused maximum-encoded-size custodyText identifier.
// Both '<' and '>' occupy six JSON bytes. Enumerating more distinct binary
// suffixes than recorded identities guarantees an unused one without trading
// away encoded bytes on collision (or imposing a fixed candidate ceiling).
func custodyForecastID(d *Document, text string) string {
	taken := make(map[string]bool, len(d.Constructions)+len(d.Consumed))
	for _, c := range d.Constructions {
		taken[c.ID] = true
	}
	for _, a := range d.Consumed {
		taken[a.ID] = true
	}
	if !taken[text] {
		return text
	}
	for n := 0; n <= len(taken); n++ {
		id := []byte(strings.Repeat("<", 256))
		for bits, i := n, len(id)-1; bits > 0; bits, i = bits>>1, i-1 {
			if bits&1 != 0 {
				id[i] = '>'
			}
		}
		if !taken[string(id)] {
			return string(id)
		}
	}
	return "" // fail closed if no candidate was found
}

// custodyPreviewForecast runs the teardown forecast on the locally loaded
// state before any request, simulating the Start the approval would commit.
// Values the approval chooses (its ID, a fresh construction ID) are at their
// custodyText maximum. A Start that would itself be refused is left to the
// authoritative Start check; only the capacity forecast is reported here.
func custodyPreviewForecast(snap Snapshot, binding Binding, p Plan, mode Mode) error {
	if mode != ModeApply && mode != ModeDeactivate && mode != ModeRecover {
		return nil
	}
	text := strings.Repeat("<", 256)
	hash := Digest(strings.Repeat("f", 64))
	var d Document
	if snap.Document == nil {
		if mode != ModeApply || !snap.Base.Pristine {
			return nil
		}
		d = emptyCustody(binding)
	} else {
		d = *snap.Document
		d.Reservations = slices.Clone(d.Reservations)
		d.Resources = slices.Clone(d.Resources)
		d.Constructions = slices.Clone(d.Constructions)
		d.Receipts = slices.Clone(d.Receipts)
		d.Consumed = slices.Clone(d.Consumed)
	}
	a := &Authorization{approval: Approval{document: ApprovalDocument{Base: snap.Base}, hash: hash}, preview: Preview{plan: p, mode: mode, hash: hash}, chain: snap.Base}
	a.approval.document.ApprovalID = custodyForecastID(&d, text)
	id := ""
	switch mode {
	case ModeApply:
		for _, c := range d.Constructions {
			if c.Mode == ModeApply && c.Input == p.hash {
				id = c.ID
			}
		}
	case ModeRecover:
		if d.Pending != nil {
			return nil // ChangeResolve settles pending; it is never refused here
		}
		for _, c := range d.Constructions {
			if !c.Complete && c.Input == p.hash {
				id = c.ID
			}
		}
		if id == "" {
			return nil
		}
	}
	a.construction = id
	if id == "" {
		id = a.approval.document.ApprovalID
	}
	if id == "" || startCustody(&d, a, id) != nil {
		return nil
	}
	// Bootstrap publishes generation 1; Commit increments after Start.
	// Match that post-Start input before reserving the combined future.
	if d.Generation == math.MaxUint64 {
		return ErrCapacity
	}
	d.Generation++
	// Bootstrap/Commit forecast their encoded-and-decoded Start, which has a
	// 64-byte checksum. A pristine synthetic Start has none: omitempty would
	// omit the entire field from both sizing paths. Reserve the same encoded
	// material here without canonicalizing or publishing this size-only state.
	// The value is not an integrity claim; authoritative codec checks stay put.
	d.Checksum = hash
	return custodyTeardownForecast(d, id)
}

func custodyChangeShape(x Change) bool {
	if !custodyDigest(x.ExpectedInventory) || !custodyText(x.ConstructionID, false) {
		return false
	}
	pointers := 0
	for _, yes := range []bool{x.Intent != nil, x.Response != nil, x.Observation != nil, x.Classification != nil, x.Receipt != nil} {
		if yes {
			pointers++
		}
	}
	switch x.Kind {
	case ChangeStart:
		return pointers == 0 && x.AttemptID == "" && x.Step == 0
	case ChangeIntent:
		return pointers == 1 && x.Intent != nil && custodyText(x.AttemptID, false) && x.Step > 0
	case ChangeResponse:
		return pointers == 1 && x.Response != nil && custodyText(x.AttemptID, false) && x.Step > 0
	case ChangeObserve:
		return pointers == 1 && x.Observation != nil && custodyText(x.AttemptID, false) && x.Step > 0
	case ChangeResolve:
		return pointers <= 1 && (pointers == 0 || x.Observation != nil) && custodyText(x.AttemptID, false) && x.Step > 0
	case ChangeClassify:
		return pointers == 1 && x.Classification != nil && x.AttemptID == "" && x.Step == 0
	case ChangeFinish:
		return pointers == 1 && x.Receipt != nil && x.AttemptID == "" && x.Step == 0
	}
	return false
}
func (s *LockedState) Commit(ctx context.Context, expected Head, a *Authorization, x Change) (Snapshot, error) {
	if e := s.enter(ctx, true); e != nil {
		return Snapshot{}, e
	}
	defer s.mu.Unlock()
	if !custodyChangeShape(x) {
		return Snapshot{}, ErrInvalid
	}
	if s.grant != nil && x.Kind != ChangeResponse && x.Kind != ChangeObserve {
		return Snapshot{}, ErrBusy
	}
	snap, e := s.load()
	if e != nil {
		return Snapshot{}, e
	}
	if snap.Document == nil || snap.Base.Head == nil || *snap.Base.Head != expected {
		return Snapshot{}, ErrConflict
	}
	if e = s.validAuth(a, snap); e != nil {
		return Snapshot{}, e
	}
	if x.ExpectedInventory != a.inventory {
		return Snapshot{}, ErrConflict
	}
	if x.Kind == ChangeStart || x.Kind == ChangeIntent || x.Kind == ChangeResolve {
		if !s.live(a) {
			return Snapshot{}, ErrExpired
		}
	}
	d := *snap.Document // Load owns every field; changes never alias caller snapshots.
	if x.Kind == ChangeStart {
		if e = startCustody(&d, a, x.ConstructionID); e != nil {
			return Snapshot{}, e
		}
	} else {
		if x.ConstructionID != a.construction {
			return Snapshot{}, ErrApproval
		}
		c := findConstruction(&d, x.ConstructionID)
		if c == nil {
			return Snapshot{}, ErrConflict
		}
		if x.Kind == ChangeResolve {
			if a.preview.mode != ModeRecover || a.started {
				return Snapshot{}, ErrApproval
			}
			if e = checkRecovery(&d, a.approval.document.Recovery, c.ID); e != nil {
				return Snapshot{}, e
			}
			if e = consumeCustody(&d, a, c.ID); e != nil {
				return Snapshot{}, e
			}
		} else if !a.started {
			return Snapshot{}, ErrApproval
		}
		if e = applyCustodyChange(&d, c, a, x, s.now()); e != nil {
			return Snapshot{}, e
		}
	}
	if d.Generation == math.MaxUint64 {
		return Snapshot{}, ErrCapacity
	}
	d.Generation++
	raw, e := custodyDocumentBytes(d)
	if e != nil {
		return Snapshot{}, e
	}
	next, e := custodyDocumentDecode(raw)
	if e != nil {
		return Snapshot{}, e
	}
	// New authority/local work reserves its remaining protocol. Results consume
	// the already-admitted slots; rerunning a fresh reservation here would
	// double-count evidence after dispatch. The actual codec cap still applies.
	if x.Kind == ChangeStart || x.Kind == ChangeIntent || x.Kind == ChangeClassify {
		if e = custodyBudget(next, x.ConstructionID); e != nil {
			return Snapshot{}, e
		}
		if x.Kind == ChangeStart {
			if e = custodyTeardownForecast(next, x.ConstructionID); e != nil {
				return Snapshot{}, e
			}
		}
	}
	if e = s.publish(ctx, "state.json", raw); e != nil {
		return Snapshot{}, e
	}
	out := snapshotOf(next)
	a.chain = baseFromWire(baseToWire(out.Base))
	switch x.Kind {
	case ChangeStart:
		a.construction = x.ConstructionID
		a.started = true
	case ChangeIntent:
		a.dispatchable[x.AttemptID] = true
	case ChangeObserve:
		a.inventory = x.Observation.Inventory
	case ChangeResolve:
		a.finished = true
	case ChangeClassify:
		a.inventory = x.Classification.Inventory
	case ChangeFinish:
		a.finished = true
	}
	return out, nil
}
func coordinateResolves(before, after Coordinate) bool {
	if !custodyCoordinate(after, false, "") {
		return false
	}
	if !before.Unassigned {
		return before == after
	}
	if before.Kind != after.Kind || before.ParentID != after.ParentID {
		return false
	}
	return before.Kind == KindDraft && after.Version == after.ID || before.Kind != KindDraft && before.Version == after.Version
}
func resolvePlanCoordinate(d *Document, p PlannedResource) (Coordinate, error) {
	c := p.Coordinate
	if p.VersionFrom != "" {
		r := findResource(d, p.VersionFrom)
		if r == nil || r.Status != ResourceOwned || r.Coordinate.Unassigned || r.Coordinate.Version == "" {
			return Coordinate{}, ErrConflict
		}
		c.Version = r.Coordinate.Version
	}
	return c, nil
}
func isConsumer(k ResourceKind) bool { return k == KindHook || k == KindRule || k == KindAction }
func resourceDisabled(d *Document, r *ResourceRecord) bool {
	if r.Status == ResourceDisabled || r.Status == ResourceRemoved {
		return true
	}
	if r.Status != ResourceRetained {
		return false
	}
	for _, c := range d.Constructions {
		for i, s := range c.Steps {
			if s.Status == StepVerified && c.Plan.Steps[i].ResourceKey == r.Key && c.Plan.Steps[i].Operation == OpDisable {
				return true
			}
		}
	}
	return false
}
func checkCleanupDelete(d *Document, c *Construction, r *ResourceRecord) error {
	for i := range d.Resources {
		other := &d.Resources[i]
		if isConsumer(other.Coordinate.Kind) && overlapCustody(other.Namespaces, c.Namespaces) && !resourceDisabled(d, other) {
			return ErrConflict
		}
		if slices.Contains(other.Dependencies, r.Key) && other.Status != ResourceRemoved {
			return ErrConflict
		}
	}
	return nil
}
func applyCustodyChange(d *Document, c *Construction, a *Authorization, x Change, now time.Time) error {
	if x.Kind == ChangeClassify {
		if c.Mode == ModeApply || d.Pending != nil || !validCustodyClassification(*x.Classification) || x.Classification.ExistingHead != *a.chain.Head {
			return ErrConflict
		}
		cl := classificationFromWire(classificationToWire(*x.Classification))
		pr := planResource(c.Plan, cl.ResourceKey)
		r := findResource(d, cl.ResourceKey)
		if pr == nil || r == nil || r.Coordinate != cl.Coordinate {
			return ErrConflict
		}
		for i, s := range c.Steps {
			if c.Plan.Steps[i].ResourceKey == r.Key && s.Status != StepPlanned && s.Status != StepClassified {
				return ErrConflict
			}
		}
		if r.Status == ResourceRemoved && cl.Reason != CleanupAlreadyAbsent {
			return ErrConflict
		}
		switch cl.Reason {
		case CleanupAlreadyAbsent:
			r.Status = ResourceRemoved
		case CleanupRetainedDependency, CleanupRetainedUnsupported:
			if cl.Observation.ManagedHash != r.ManagedHash || cl.Observation.ServerRevision != r.ServerRevision {
				return ErrConflict
			}
			r.Status = ResourceRetained
		case CleanupUnresolved:
		}
		if cl.Observation != nil {
			r.Observations = append(r.Observations, *cl.Observation)
		}
		c.Classifications = append(c.Classifications, cl)
		for i := range c.Steps {
			if c.Plan.Steps[i].ResourceKey == r.Key && c.Steps[i].Status == StepPlanned {
				c.Steps[i].Status = StepClassified
				c.Steps[i].Evidence = append([]Evidence{}, cl.Evidence...)
			}
		}
		return nil
	}
	if x.Kind == ChangeFinish {
		return finishCustody(d, c, a, *x.Receipt, now)
	}
	if x.Step == 0 || int(x.Step) > len(c.Steps) {
		return ErrInvalid
	}
	index := int(x.Step) - 1
	step := &c.Steps[index]
	ps := c.Plan.Steps[index]
	pr := planResource(c.Plan, ps.ResourceKey)
	r := findResource(d, ps.ResourceKey)
	if pr == nil || r == nil {
		return ErrCorrupt
	}
	if x.Kind == ChangeIntent {
		if d.Pending != nil || step.Status != StepPlanned || c.Complete || !custodySpec(x.Intent.Desired) || !custodyDigest(x.Intent.PreObservation) {
			return ErrConflict
		}
		for _, other := range d.Constructions {
			for _, s := range other.Steps {
				if s.AttemptID == x.AttemptID {
					return ErrConflict
				}
			}
		}
		for i := 0; i < index; i++ {
			// Independently resolved noncommit settles ordering, not ownership.
			// Dependency admission below still rejects its tombstone, and the
			// attempted step itself can never return to StepPlanned.
			if c.Steps[i].Status != StepVerified && c.Steps[i].Status != StepClassified && c.Steps[i].Status != StepNotApplied {
				return ErrConflict
			}
		}
		if len(x.Intent.Dependencies) != len(pr.Dependencies) {
			return ErrConflict
		}
		for i, key := range pr.Dependencies {
			dep := findResource(d, key)
			if dep == nil || dep.Coordinate != x.Intent.Dependencies[i] || dep.Status == ResourceReserved || dep.Status == ResourceNotApplied {
				return ErrConflict
			}
		}
		coordinate, e := resolvePlanCoordinate(d, *pr)
		if e != nil {
			return e
		}
		if c.Mode == ModeApply {
			if r.Status != ResourceReserved {
				return ErrConflict
			}
			for _, key := range r.Dependencies {
				dep := findResource(d, key)
				if dep == nil || dep.Status != ResourceOwned {
					return ErrConflict
				}
			}
		} else {
			if r.Status == ResourceRemoved || r.Status == ResourceReserved || r.Status == ResourceNotApplied {
				return ErrConflict
			}
			if ps.Operation == OpDelete {
				if e = checkCleanupDelete(d, c, r); e != nil {
					return e
				}
			}
		}
		step.Status = StepIntent
		step.AttemptID = x.AttemptID
		step.Coordinate = coordinate
		if r.FirstAttempt == "" {
			r.FirstAttempt = x.AttemptID
		}
		invalidateCustody(d, c.Namespaces, x.AttemptID)
		d.Pending = &Pending{AttemptID: x.AttemptID, ConstructionID: c.ID, ResourceKey: r.Key, BindingDigest: custodyBindingHash(d.Binding), Input: c.Input, Preview: a.preview.hash, OriginalApproval: a.approval.hash, IntentHead: *a.chain.Head, Namespaces: append([]string{}, c.Namespaces...), Coordinate: coordinate, Operation: ps.Operation, Method: custodyMethod(coordinate.Kind, ps.Operation), Desired: x.Intent.Desired, PreInventory: a.inventory, PreObservation: x.Intent.PreObservation, Phase: StepIntent}
		return nil
	}
	pending := d.Pending
	if pending == nil || pending.ConstructionID != c.ID || pending.AttemptID != x.AttemptID || step.AttemptID != x.AttemptID {
		return ErrConflict
	}
	if x.Kind == ChangeResponse {
		if !a.dispatched[x.AttemptID] || pending.Phase != StepIntent || !validCustodyResponse(*x.Response) || !coordinateResolves(pending.Coordinate, x.Response.Coordinate) {
			return ErrConflict
		}
		resolved := x.Response.Coordinate
		for _, old := range d.Resources {
			if old.Key != r.Key && custodyIdentity(old.Coordinate) == custodyIdentity(resolved) {
				return ErrConflict
			}
		}
		pending.Returned = responseFromWire(responseToWire(x.Response))
		pending.Phase = StepResponse
		step.Status = StepResponse
		step.Coordinate = resolved
		if pending.Coordinate.Unassigned {
			r.Coordinate = resolved
		}
		return nil
	}
	if x.Kind == ChangeResolve {
		rec := a.approval.document.Recovery
		if rec == nil || rec.AttemptID != x.AttemptID {
			return ErrApproval
		}
		if rec.Disposition == DispositionNotApplied {
			if x.Observation != nil {
				return ErrApproval
			}
			step.Status = StepNotApplied
			step.Evidence = append([]Evidence{}, rec.Attribution...)
			if c.Mode == ModeApply {
				r.Status = ResourceNotApplied
			}
			d.Pending = nil
			return nil
		}
		if x.Observation == nil || x.Observation.Coordinate != rec.Coordinate || x.Observation.ManagedHash != rec.ExpectedManagedHash || x.Observation.ExactRead != rec.ExactRead || x.Observation.Inventory != rec.Inventory {
			return ErrApproval
		}
	} else if x.Kind != ChangeObserve || pending.Phase != StepResponse || !a.dispatched[x.AttemptID] {
		return ErrConflict
	}
	o := x.Observation
	if o == nil || !custodyObservation(*o) || !coordinateResolves(pending.Coordinate, o.Coordinate) || pending.Returned != nil && o.Coordinate != pending.Returned.Coordinate {
		return ErrConflict
	}
	if pending.Returned != nil && pending.Returned.ManagedHash != nil && *pending.Returned.ManagedHash != o.ManagedHash {
		return ErrConflict
	}
	if pending.Operation == OpDelete {
		if o.Presence != PresenceAbsent {
			return ErrConflict
		}
		r.Status = ResourceRemoved
	} else {
		if o.Presence != PresencePresent {
			return ErrConflict
		}
		if pending.Operation == OpDisable {
			r.Status = ResourceDisabled
		} else {
			r.Status = ResourceOwned
		}
		r.ManagedHash = o.ManagedHash
		r.ServerRevision = o.ServerRevision
	}
	for _, old := range d.Resources {
		if old.Key != r.Key && custodyIdentity(old.Coordinate) == custodyIdentity(o.Coordinate) {
			return ErrConflict
		}
	}
	if len(r.Acquisition) == 0 {
		if x.Kind == ChangeResolve {
			r.Acquisition = append([]Evidence{}, a.approval.document.Recovery.Attribution...)
		} else {
			r.Acquisition = append([]Evidence{}, pending.Returned.Evidence...)
		}
	}
	r.Coordinate = o.Coordinate
	step.Coordinate = o.Coordinate
	step.Status = StepVerified
	step.Evidence = append([]Evidence{}, o.Evidence...)
	r.Observations = append(r.Observations, observationFromWire(observationToWire(*o)))
	d.Pending = nil
	return nil
}
func finishCustody(d *Document, c *Construction, a *Authorization, in Receipt, now time.Time) error {
	if d.Pending != nil || !custodyText(in.ID, false) || in.InvalidatedBy != "" || in.ConstructionID != c.ID || in.Mode != c.Mode || in.Input != c.Input || in.Preview != a.preview.hash || in.OperatorApproval != a.approval.hash || in.BindingDigest != custodyBindingHash(d.Binding) || in.Inventory != a.inventory || !slices.Equal(in.Namespaces, c.Namespaces) || in.ObservedMS < a.approval.document.IssuedMS || in.ObservedMS > now.UnixMilli() || len(in.Members) != len(c.Plan.Resources) {
		return ErrConflict
	}
	for _, old := range d.Receipts {
		if old.ID == in.ID {
			return ErrConflict
		}
	}
	settled := true
	for _, step := range c.Steps {
		if step.Status != StepVerified && step.Status != StepClassified {
			return ErrConflict
		}
	}
	seen := map[string]bool{}
	allAbsent, allDisabled, unresolved := true, true, false
	for _, m := range in.Members {
		if !validCustodyMember(m) || seen[m.ResourceKey] || planResource(c.Plan, m.ResourceKey) == nil {
			return ErrConflict
		}
		seen[m.ResourceKey] = true
		r := findResource(d, m.ResourceKey)
		if r == nil || r.Coordinate != m.Coordinate {
			return ErrConflict
		}
		if m.Observation != nil {
			if m.Observation.Inventory != in.Inventory {
				return ErrConflict
			}
			if m.Observation.Presence == PresencePresent && (r.Status == ResourceRemoved || m.Observation.ManagedHash != r.ManagedHash || m.Observation.ServerRevision != r.ServerRevision) {
				return ErrConflict
			}
		}
		switch m.Outcome {
		case OutcomePresent:
			if c.Mode != ModeApply || r.Status != ResourceOwned {
				return ErrConflict
			}
			allAbsent = false
		case OutcomePresentDisabled:
			if c.Mode == ModeApply || !isConsumer(r.Coordinate.Kind) || !resourceDisabled(d, r) {
				return ErrConflict
			}
			allAbsent = false
		case OutcomeRemoved:
			if c.Mode == ModeApply || r.Status != ResourceRemoved {
				return ErrConflict
			}
			found := false
			for i, s := range c.Steps {
				if c.Plan.Steps[i].ResourceKey == r.Key && c.Plan.Steps[i].Operation == OpDelete && s.Status == StepVerified {
					found = true
				}
			}
			if !found {
				return ErrConflict
			}
		case OutcomeAlreadyAbsent, OutcomeRetainedDependency, OutcomeRetainedUnsupported, OutcomeUnresolved:
			if c.Mode == ModeApply {
				return ErrConflict
			}
			var cl *CleanupClassification
			for i := range c.Classifications {
				if c.Classifications[i].ResourceKey == r.Key {
					cl = &c.Classifications[i]
				}
			}
			if cl == nil || string(cl.Reason) != string(m.Outcome) {
				return ErrConflict
			}
			if m.Outcome == OutcomeAlreadyAbsent {
				if r.Status != ResourceRemoved {
					return ErrConflict
				}
			} else {
				allAbsent = false
			}
			if m.Outcome == OutcomeUnresolved {
				unresolved = true
				settled = false
			}
		default:
			return ErrConflict
		}
		if isConsumer(r.Coordinate.Kind) && !resourceDisabled(d, r) {
			allDisabled = false
		}
	}
	switch c.Mode {
	case ModeApply:
		if in.Result != ReceiptApplyObserved || unresolved {
			return ErrConflict
		}
	case ModeTeardown:
		if allAbsent {
			if in.Result != ReceiptFullyRemoved {
				return ErrConflict
			}
		} else if in.Result != ReceiptIncomplete {
			return ErrConflict
		}
	case ModeDeactivate:
		if allDisabled && !unresolved {
			if in.Result != ReceiptDeactivated {
				return ErrConflict
			}
		} else if in.Result != ReceiptIncomplete {
			return ErrConflict
		}
	default:
		return ErrConflict
	}
	// Reconstruct caller-owned receipt pointers through the private codec.
	wire := documentToWire(Document{Receipts: []Receipt{in}})
	copy, e := documentFromWire(wire)
	if e != nil {
		return e
	}
	d.Receipts = append(d.Receipts, copy.Receipts[0])
	c.Complete = settled
	return nil
}
func (s *LockedState) ClaimDispatch(ctx context.Context, expected Head, a *Authorization, attempt string) (*DispatchGrant, error) {
	if e := s.enter(ctx, true); e != nil {
		return nil, e
	}
	defer s.mu.Unlock()
	if s.grant != nil {
		return nil, ErrBusy
	}
	snap, e := s.load()
	if e != nil {
		return nil, e
	}
	if snap.Base.Head == nil || *snap.Base.Head != expected {
		return nil, ErrConflict
	}
	if s.validAuth(a, snap) != nil || !a.started || !a.dispatchable[attempt] || !s.live(a) {
		return nil, ErrApproval
	}
	p := snap.Document.Pending
	if p == nil || p.AttemptID != attempt || p.Phase != StepIntent || p.OriginalApproval != a.approval.hash {
		return nil, ErrConflict
	}
	a.dispatchable[attempt] = false
	a.dispatched[attempt] = true
	deadline := time.UnixMilli(a.approval.document.ExpiresMS)
	if inv, ok := s.ctx.Deadline(); ok && inv.Before(deadline) {
		deadline = inv
	}
	request, cancel := context.WithDeadline(ctx, deadline)
	stop := context.AfterFunc(s.ctx, cancel)
	if e = request.Err(); e != nil {
		cancel()
		stop()
		return nil, e
	}
	g := &DispatchGrant{owner: s, attempt: attempt, ctx: request, cancel: func() { stop(); cancel() }}
	g.self = g
	s.grant = g
	return g, nil
}
func (g *DispatchGrant) Context() context.Context {
	cancelled := func() context.Context { c, cancel := context.WithCancel(context.Background()); cancel(); return c }
	if g == nil || g.self != g || g.owner == nil {
		return cancelled()
	}
	s := g.owner
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.self != s || s.pid != os.Getpid() || s.closed || g.released || s.grant != g {
		return cancelled()
	}
	return g.ctx
}
func (s *LockedState) ReleaseDispatch(g *DispatchGrant) error {
	if s == nil || s.self != s || s.pid != os.Getpid() {
		return ErrInvalid
	}
	if !s.mu.TryLock() {
		return ErrBusy
	}
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if g == nil || g.self != g || g.owner != s || s.grant != g || g.released {
		return ErrInvalid
	}
	// This is the trusted caller's assertion AFTER positive local sender join.
	g.cancel()
	g.released = true
	s.grant = nil
	return nil
}
func (s *LockedState) Close() error {
	if s == nil || s.self != s || s.pid != os.Getpid() {
		return ErrInvalid
	}
	if !s.mu.TryLock() {
		return ErrBusy
	}
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if s.grant != nil {
		return ErrBusy
	}
	s.closed = true
	s.cancel()
	e1 := s.lock.Close()
	e2 := s.dir.Close()
	if e1 != nil || e2 != nil {
		return ErrIO
	}
	return nil
}
