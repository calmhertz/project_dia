package domain

import (
	"time"

	"github.com/google/uuid"
)

type UserRole string

const (
	RoleRoot  UserRole = "root"
	RoleAdmin UserRole = "admin"
	RoleUser  UserRole = "user"
)

type RFBand string

const (
	BandVHF RFBand = "vhf"
	BandUHF RFBand = "uhf"
)

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

type PassVisibility string

const (
	VisibilityPrivate PassVisibility = "private"
	VisibilityPublic  PassVisibility = "public"
)

type CancellationReason string

const (
	CancelledByOwner        CancellationReason = "cancelled_by_owner"
	CancelledByAdmin        CancellationReason = "cancelled_by_admin"
	CancelledByRootOverride CancellationReason = "cancelled_by_root_override"
)

type RecordingMode string

const (
	RecordRaw           RecordingMode = "raw"
	RecordProcess       RecordingMode = "process"
	RecordRawAndProcess RecordingMode = "raw_and_process"
)

type TLESource string

const (
	SourceCelestrak TLESource = "celestrak"
	SourceSatNOGS   TLESource = "satnogs"
	SourceManual    TLESource = "manual"
)

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
	PendingUploads int
	PendingReports int
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

type StationHardwareConfig struct {
	StationID                   uuid.UUID
	RotatorSerialPort           string
	RotatorBaudRate             int32
	RotatorParkAzimuthDegrees   int32
	RotatorParkElevationDegrees int32
	SDRDeviceIdentifier         string
}
