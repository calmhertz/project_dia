package workerapi

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"aagasa/internal/filestore"
	workerv1 "aagasa/internal/gen/worker/v1"
	"aagasa/internal/passplan"
	"aagasa/internal/store"
)

const authorizationHeader = "authorization"
const bearerPrefix = "Bearer "

type Service struct {
	workerv1.UnimplementedWorkerServiceServer

	repo          *store.Repository
	files         *filestore.Store
	logger        *slog.Logger
	serverVersion string
}

func NewService(repo *store.Repository, files *filestore.Store,
	logger *slog.Logger, serverVersion string) *Service {
	return &Service{repo: repo, files: files, logger: logger, serverVersion: serverVersion}
}

func (s *Service) SyncPassPlans(ctx context.Context, request *workerv1.SyncPassPlansRequest) (*workerv1.SyncPassPlansResponse, error) {
	stationID, err := uuid.Parse(request.GetStationId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid station id")
	}

	stored, err := s.repo.ListPassPlansForStation(ctx, stationID, time.Now().UTC())
	if err != nil {
		s.logger.Error("load pass plans", slog.String("error", err.Error()))
		return nil, status.Error(codes.Internal, "could not load pass plans")
	}

	plans := make([]*workerv1.PassPlan, 0, len(stored))
	for _, record := range stored {
		plan, err := passplan.Unmarshal(record.Encoded)
		if err != nil {
			s.logger.Error("decode stored pass plan",
				slog.String("pass_id", record.PassID.String()),
				slog.String("error", err.Error()))
			return nil, status.Error(codes.Internal, "a stored pass plan is unreadable")
		}
		plans = append(plans, plan)
	}

	generation := passplan.SetGeneration(plans)
	if generation == request.GetCurrentGeneration() {
		return &workerv1.SyncPassPlansResponse{Changed: false}, nil
	}

	s.logger.Info("sending desired state",
		slog.String("worker_id", request.GetWorkerId()),
		slog.Int("plans", len(plans)),
		slog.String("generation", generation))

	return &workerv1.SyncPassPlansResponse{
		Changed: true,
		Plans: &workerv1.PassPlanSet{
			Plans:       plans,
			Generation:  generation,
			GeneratedAt: timestamppb.New(time.Now().UTC()),
		},
	}, nil
}

func (s *Service) Ping(ctx context.Context, request *workerv1.PingRequest) (*workerv1.PingResponse, error) {
	s.logger.Debug("worker ping",
		slog.String("worker_id", request.GetWorkerId()),
		slog.String("station_id", request.GetStationId()),
		slog.String("worker_version", request.GetWorkerVersion()),
	)
	return &workerv1.PingResponse{
		ServerTimeUnixMs: time.Now().UnixMilli(),
		ServerVersion:    s.serverVersion,
	}, nil
}

func SharedSecretInterceptor(sharedSecret string) grpc.UnaryServerInterceptor {
	expected := []byte(sharedSecret)
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if !validSecret(ctx, expected) {
			return nil, status.Error(codes.Unauthenticated, "invalid worker credential")
		}
		return handler(ctx, request)
	}
}

func validSecret(ctx context.Context, expected []byte) bool {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return false
	}
	values := md.Get(authorizationHeader)
	if len(values) != 1 {
		return false
	}
	presented := strings.TrimPrefix(values[0], bearerPrefix)
	return subtle.ConstantTimeCompare([]byte(presented), expected) == 1
}
