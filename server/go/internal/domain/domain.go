// Package domain holds Aagasa's core entity types.
//
// These mirror the PostgreSQL schema in internal/store/migrations. Behaviour
// (authentication, approval, scheduling) belongs to later phases; this package
// only describes what is stored.
package domain

import (
	"time"

	"github.com/google/uuid"
)

// UserRole is a user's authority level (spec.md section 17).
type UserRole string

const (
	RoleRoot  UserRole = "root"
	RoleAdmin UserRole = "admin"
	RoleUser  UserRole = "user"
)

// RFBand is a station RF chain. Only one is active at a time.
type RFBand string

const (
	BandVHF RFBand = "vhf"
	BandUHF RFBand = "uhf"
)

// PassStatus is the externally meaningful pass lifecycle (spec.md section 14).
type PassStatus string

const (
	PassPendingApproval PassStatus = "pending_approval"
	PassApproved        PassStatus = "approved"
	PassRejected        PassStatus = "rejected"
	PassCancelled       PassStatus = "cancelled"
	PassExecuting       PassStatus = "executing"
	PassCompleted       PassStatus = "completed"
	PassFailed          PassStatus = "failed"
	PassMissed          PassStatus = "missed"
)

// PassVisibility controls who may see a pass and its results.
type PassVisibility string

const (
	VisibilityPrivate PassVisibility = "private"
	VisibilityPublic  PassVisibility = "public"
)

// CancellationReason records why an approved pass stopped being executable.
type CancellationReason string

const (
	CancelledByOwner        CancellationReason = "cancelled_by_owner"
	CancelledByAdmin        CancellationReason = "cancelled_by_admin"
	CancelledByRootOverride CancellationReason = "cancelled_by_root_override"
)

// RecordingMode is what a pass produces.
type RecordingMode string

const (
	RecordRaw           RecordingMode = "raw"
	RecordProcess       RecordingMode = "process"
	RecordRawAndProcess RecordingMode = "raw_and_process"
)

// TLESource identifies where orbital data came from.
type TLESource string

const (
	SourceCelestrak TLESource = "celestrak"
	SourceSatNOGS   TLESource = "satnogs"
	SourceManual    TLESource = "manual"
)

// WorkerConnectionState is the Server's view of Worker reachability.
type WorkerConnectionState string

const (
	WorkerOnline  WorkerConnectionState = "online"
	WorkerOffline WorkerConnectionState = "offline"
)

type User struct {
	ID                 uuid.UUID
	Username           string
	PasswordHash       string
	Role               UserRole
	MustChangePassword bool
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type Station struct {
	ID           uuid.UUID
	Name         string
	Latitude     float64
	Longitude    float64
	AltitudeM    float64
	Timezone     string
	ActiveRFBand RFBand
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// SchedulingConfig is versioned: a change inserts a new row rather than
// mutating the current one (spec.md sections 13.6 and 13.8).
type SchedulingConfig struct {
	ID                      uuid.UUID
	StationID               uuid.UUID
	MinimumLeadTime         time.Duration
	PrePassBuffer           time.Duration
	PostPassBuffer          time.Duration
	RecordingPreRoll        time.Duration
	RecordingPostRoll       time.Duration
	MinimumElevationDegrees float64
	CreatedBy               *uuid.UUID
	CreatedAt               time.Time
	EffectiveFrom           time.Time
}

type Worker struct {
	ID                     uuid.UUID
	StationID              uuid.UUID
	Name                   string
	WorkerVersion          string
	ConnectionState        WorkerConnectionState
	LastSeenAt             *time.Time
	LastSyncAt             *time.Time
	DesiredStateGeneration int64
	// What the Worker last reported it was still carrying.
	PendingUploads int
	PendingReports int
	// Desired-state generation the Worker has confirmed it holds.
	SyncedGeneration string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type Satellite struct {
	ID            uuid.UUID
	NoradID       int
	Name          string
	Description   string
	Metadata      map[string]any
	IsSchedulable bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type TLERecord struct {
	ID          uuid.UUID
	SatelliteID uuid.UUID
	Line1       string
	Line2       string
	Epoch       time.Time
	Source      TLESource
	FetchedAt   time.Time
}

type Pass struct {
	ID                 uuid.UUID
	StationID          uuid.UUID
	SatelliteID        uuid.UUID
	RequestedBy        uuid.UUID
	TLERecordID        uuid.UUID
	SchedulingConfigID uuid.UUID

	Status     PassStatus
	Visibility PassVisibility
	Band       RFBand

	AOSAt               time.Time
	LOSAt               time.Time
	TCAAt               *time.Time
	MaxElevationDegrees float64

	// Resource reservation, distinct from the recording margins below.
	ReservedFrom time.Time
	ReservedTo   time.Time

	RecordingMode     RecordingMode
	RecordingPreRoll  time.Duration
	RecordingPostRoll time.Duration
	PipelineID        *uuid.UUID
	RadioSettings     map[string]any

	ApprovedBy         *uuid.UUID
	ApprovedAt         *time.Time
	CancellationReason *CancellationReason
	CancelledBy        *uuid.UUID
	CancelledAt        *time.Time

	CreatedAt time.Time
	UpdatedAt time.Time
}

// AuditRecord is append-only; the database rejects updates and deletes.
type AuditRecord struct {
	ID            int64
	ActorUserID   *uuid.UUID
	Action        string
	EntityType    string
	EntityID      string
	PreviousState map[string]any
	NewState      map[string]any
	Reason        string
	CreatedAt     time.Time
}

// StationBandConfig is the antenna and radio setup for one RF band.
type StationBandConfig struct {
	StationID          uuid.UUID
	Band               RFBand
	AntennaDescription string
	CenterFrequencyHz  *int64
	SampleRateHz       *int32
	GainDB             *float64
	PPMCorrection      int32
	BiasTeeEnabled     bool
}

// StationHardwareConfig is the rotator and SDR setup for a station.
type StationHardwareConfig struct {
	StationID                   uuid.UUID
	RotatorSerialPort           string
	RotatorBaudRate             int32
	RotatorParkAzimuthDegrees   int32
	RotatorParkElevationDegrees int32
	SDRDeviceIdentifier         string
}
