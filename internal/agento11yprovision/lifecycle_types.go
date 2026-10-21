// SPDX-License-Identifier: AGPL-3.0-only

package agento11yprovision

import (
	"errors"
	"github.com/rknightion/synthkit/internal/core"
)

// The lifecycle records are private custody, not HTTP DTOs or runtime admission.
// Only the private wire codec in plan.go defines their persisted representation.
type Digest string
type Mode string

const (
	ModeApply      Mode = "apply"
	ModeRecover    Mode = "recover"
	ModeDeactivate Mode = "deactivate"
	ModeTeardown   Mode = "teardown"
)

type ResourceKind string

const (
	KindEvaluator   ResourceKind = "evaluator"
	KindHook        ResourceKind = "hook"
	KindRule        ResourceKind = "rule"
	KindCollection  ResourceKind = "collection"
	KindAction      ResourceKind = "action"
	KindSuite       ResourceKind = "suite"
	KindDraft       ResourceKind = "draft"
	KindCase        ResourceKind = "case"
	KindBindings    ResourceKind = "bindings"
	KindPublication ResourceKind = "publication"
)

type Operation string

const (
	OpCreate          Operation = "create"
	OpInitialBindings Operation = "initial_bindings"
	OpPublish         Operation = "publish"
	OpDisable         Operation = "disable"
	OpDelete          Operation = "delete"
)

type Destination struct{ GrafanaOrigin, DeploymentPath, GrafanaOrg, PluginRoute, EffectiveTenant, RuntimeEndpoint, RuntimeTenant string }
type Binding struct {
	DeploymentID, StateID, ContractRevision, SourceRevision string
	Destination                                             Destination
}
type Location struct {
	Directory      string
	ExpectEnrolled bool
}
type Head struct {
	Generation uint64
	Digest     Digest
}
type Base struct {
	Pristine bool
	Head     *Head
}
type Snapshot struct {
	Base     Base
	Document *Document
}
type Origin struct {
	Blueprint, Instance string
	Declaration         uint32
}
type DeclarationInput struct {
	Origin      Origin
	Declaration core.AgentDeclaration
}
type PlanInput struct {
	Binding      Binding
	Mode         Mode
	Declarations []DeclarationInput
	Targets      []Origin
	Resources    []PlannedResource
	Steps        []PlannedStep
}
type Coordinate struct {
	Kind                  ResourceKind
	ID, ParentID, Version string
	Unassigned            bool
}
type PlannedResource struct {
	Key, VersionFrom         string
	Origin                   Origin
	Coordinate               Coordinate
	Spec                     AuthoredSpec
	Dependencies, Namespaces []string
}
type PlannedStep struct {
	Number      uint32
	ResourceKey string
	Operation   Operation
}
type Evidence struct {
	ID     string
	Digest Digest
}
type ForeignCoverage struct {
	Coordinate  Coordinate
	ManagedHash Digest
	Reason      string
	Evidence    []Evidence
}
type Assertions struct {
	AuthorizedOperator, EffectiveDestination, AllAffectedEmittersStopped, KeepStoppedUntilReceiptAccepted, ExclusiveNamespace, OutstandingWorkMayFinish, NonAtomicInitialPublication, PermanentRecordsMayRemain, NoSyntheticReentry bool
	DestinationEvidence, RuntimeEvidence, StopEvidence, NamespaceEvidence                                                                                                                                                           []Evidence
	Foreign                                                                                                                                                                                                                         []ForeignCoverage
}
type Disposition string

const (
	DispositionObserved   Disposition = "observed"
	DispositionNotApplied Disposition = "not_applied"
)

type Recovery struct {
	AttemptID                                               string
	OriginalInput, OriginalPreview, OriginalApproval        Digest
	ResourceKey                                             string
	Coordinate                                              Coordinate
	Operation                                               Operation
	AuthoredHash, ExpectedManagedHash, ExactRead, Inventory Digest
	Attribution                                             []Evidence
	Disposition                                             Disposition
}
type ApprovalDocument struct {
	Schema                               uint32
	ApprovalID, Operator, ConstructionID string
	IssuedMS, ExpiresMS                  int64
	Binding                              Binding
	Input, Preview, Inventory            Digest
	Base                                 Base
	Mode                                 Mode
	Namespaces                           []string
	Assertions                           Assertions
	Recovery                             *Recovery
}
type Reservation struct {
	Namespace, WireName string
	Origin              Origin
	FirstConstruction   string
	Approval            Digest
}
type ResourceStatus string

const (
	ResourceReserved   ResourceStatus = "reserved"
	ResourceOwned      ResourceStatus = "owned"
	ResourceDisabled   ResourceStatus = "disabled"
	ResourceRetained   ResourceStatus = "retained"
	ResourceRemoved    ResourceStatus = "removed"
	ResourceNotApplied ResourceStatus = "not_applied"
)

type ResourceRecord struct {
	Key                      string
	Origin                   Origin
	Coordinate               Coordinate
	FirstAttempt             string
	Authored                 AuthoredSpec
	Dependencies, Namespaces []string
	ManagedHash              Digest
	ServerRevision           string
	Acquisition              []Evidence
	Observations             []Observation
	Status                   ResourceStatus
}
type StepStatus string

const (
	StepPlanned    StepStatus = "planned"
	StepIntent     StepStatus = "intent"
	StepResponse   StepStatus = "response"
	StepVerified   StepStatus = "verified"
	StepNotApplied StepStatus = "not_applied"
	StepClassified StepStatus = "classified"
)

type StepRecord struct {
	Number     uint32
	AttemptID  string
	Status     StepStatus
	Coordinate Coordinate
	Evidence   []Evidence
}
type Construction struct {
	ID                       string
	Input, Preview, Approval Digest
	Mode                     Mode
	Base                     Base
	Namespaces               []string
	Plan                     PlanInput
	Steps                    []StepRecord
	Classifications          []CleanupClassification
	Complete                 bool
}
type IntentEvidence struct {
	Desired        AuthoredSpec
	PreObservation Digest
	Dependencies   []Coordinate
}
type ResponseEvidence struct {
	Coordinate  Coordinate
	Evidence    []Evidence
	ManagedHash *Digest
}
type Pending struct {
	AttemptID, ConstructionID, ResourceKey          string
	BindingDigest, Input, Preview, OriginalApproval Digest
	IntentHead                                      Head
	Namespaces                                      []string
	Coordinate                                      Coordinate
	Operation                                       Operation
	Method                                          string
	Desired                                         AuthoredSpec
	PreInventory, PreObservation                    Digest
	Phase                                           StepStatus
	Returned                                        *ResponseEvidence
}
type Presence string

const (
	PresencePresent Presence = "present"
	PresenceAbsent  Presence = "absent"
)

type Observation struct {
	Coordinate                        Coordinate
	Presence                          Presence
	ManagedHash, ExactRead, Inventory Digest
	ServerRevision                    string
	Evidence                          []Evidence
}
type CleanupReason string

const (
	CleanupRetainedDependency  CleanupReason = "retained_dependency"
	CleanupRetainedUnsupported CleanupReason = "retained_unsupported"
	CleanupAlreadyAbsent       CleanupReason = "already_absent"
	CleanupUnresolved          CleanupReason = "unresolved"
)

type CleanupClassification struct {
	ResourceKey  string
	Coordinate   Coordinate
	ExistingHead Head
	Reason       CleanupReason
	Observation  *Observation
	Inventory    Digest
	Evidence     []Evidence
}
type MemberOutcome string

const (
	OutcomePresent             MemberOutcome = "present"
	OutcomePresentDisabled     MemberOutcome = "present_disabled"
	OutcomeRemoved             MemberOutcome = "removed"
	OutcomeAlreadyAbsent       MemberOutcome = "already_absent"
	OutcomeRetainedDependency  MemberOutcome = "retained_dependency"
	OutcomeRetainedUnsupported MemberOutcome = "retained_unsupported"
	OutcomeUnresolved          MemberOutcome = "unresolved"
)

type ReceiptMember struct {
	ResourceKey string
	Coordinate  Coordinate
	Outcome     MemberOutcome
	Observation *Observation
	Evidence    []Evidence
}
type ReceiptResult string

const (
	ReceiptApplyObserved ReceiptResult = "apply_observed"
	ReceiptDeactivated   ReceiptResult = "deactivated"
	ReceiptFullyRemoved  ReceiptResult = "fully_removed"
	ReceiptIncomplete    ReceiptResult = "incomplete"
)

type Receipt struct {
	ID, ConstructionID                       string
	Mode                                     Mode
	Result                                   ReceiptResult
	BindingDigest, Input, Preview, Inventory Digest
	Namespaces                               []string
	Members                                  []ReceiptMember
	OperatorApproval                         Digest
	ObservedMS                               int64
	InvalidatedBy                            string
}
type ConsumedApproval struct {
	ID             string
	Digest         Digest
	ConstructionID string
	Base           Base
}
type Document struct {
	Schema        uint32
	Binding       Binding
	Generation    uint64
	Checksum      Digest
	Reservations  []Reservation
	Resources     []ResourceRecord
	Constructions []Construction
	Receipts      []Receipt
	Consumed      []ConsumedApproval
	Pending       *Pending
}
type ChangeKind string

const (
	ChangeStart    ChangeKind = "start"
	ChangeIntent   ChangeKind = "intent"
	ChangeResponse ChangeKind = "response"
	ChangeObserve  ChangeKind = "observe"
	ChangeResolve  ChangeKind = "resolve"
	ChangeClassify ChangeKind = "classify"
	ChangeFinish   ChangeKind = "finish"
)

type Change struct {
	Kind                      ChangeKind
	ExpectedInventory         Digest
	ConstructionID, AttemptID string
	Step                      uint32
	Intent                    *IntentEvidence
	Response                  *ResponseEvidence
	Observation               *Observation
	Classification            *CleanupClassification
	Receipt                   *Receipt
}

var (
	ErrInvalid         = errors.New("custody invalid")
	ErrBinding         = errors.New("custody binding mismatch")
	ErrCorrupt         = errors.New("custody corrupt")
	ErrEnrolledMissing = errors.New("custody enrolled state missing")
	ErrCapacity        = errors.New("custody capacity")
	ErrLockTimeout     = errors.New("custody lock timeout")
	ErrConflict        = errors.New("custody conflict")
	ErrApproval        = errors.New("custody approval invalid")
	ErrExpired         = errors.New("custody approval expired")
	ErrConsumed        = errors.New("custody approval consumed")
	ErrClosed          = errors.New("custody closed")
	ErrBusy            = errors.New("custody busy")
	ErrOutcomeUnknown  = errors.New("custody durability outcome unknown")
	ErrUnsupported     = errors.New("custody filesystem unsupported")
	ErrIO              = errors.New("custody IO failed")
)

const (
	custodyMaxBytes     = 768 << 10
	custodySpecBytes    = 64 << 10
	custodyMaxResources = 256
	custodyMaxClaims    = 256
	custodyMaxSteps     = 512
)

type AuthoredSpec struct {
	raw  []byte
	hash Digest
}
type Plan struct {
	input  PlanInput
	names  []string
	claims []Reservation
	hash   Digest
}
type Preview struct {
	plan      Plan
	mode      Mode
	base      Base
	inventory Digest
	raw       []byte
	hash      Digest
}
type Approval struct {
	document ApprovalDocument
	hash     Digest
}

func (s AuthoredSpec) Digest() Digest { return s.hash }
func (s AuthoredSpec) JSON() []byte   { return append([]byte(nil), s.raw...) }
func (p Plan) Digest() Digest         { return p.hash }
func (p Preview) Digest() Digest      { return p.hash }
func (p Preview) JSON() []byte        { return append([]byte(nil), p.raw...) }
