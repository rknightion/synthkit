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
	raw, e := s.read("state.json", custodyMaxBytes)
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
	check, e := s.read(name, custodyMaxBytes)
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
	check := func() error {
		raw, err := json.Marshal(w)
		if err != nil {
			return ErrCorrupt
		}
		if len(raw)-alternativeCredit > custodyMaxBytes {
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
	if err := check(); err != nil {
		return err
	}
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
		// Allow independent recovery attribution plus a fresh suffix approval.
		// Their documents are not stored, only these bounded consumed records.
		for range 2 {
			w.Consumed = append(w.Consumed, consumedWire{text, hash, c.ID, baseWire{Head: &maxHead}})
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
		r.Observations = append(r.Observations, o)
		step.Status, step.Coordinate, step.Evidence = StepNotApplied, o.Coordinate, es
		w.Pending = nil
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
	w.Receipts = append(w.Receipts, receiptWire{text, c.ID, c.Mode, ReceiptApplyObserved, hash, c.Input, hash, hash, c.Namespaces, members, hash, math.MaxInt64, text})
	return check()
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
