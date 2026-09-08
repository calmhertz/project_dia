// Command aagasa-server runs the Aagasa control plane.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"aagasa/internal/auth"
	"aagasa/internal/catalog"
	"aagasa/internal/config"
	"aagasa/internal/filestore"
	workerv1 "aagasa/internal/gen/worker/v1"
	"aagasa/internal/httpapi"
	"aagasa/internal/logging"
	"aagasa/internal/passplan"
	"aagasa/internal/prediction"
	"aagasa/internal/scheduling"
	"aagasa/internal/store"
	"aagasa/internal/tle"
	"aagasa/internal/workerapi"
)

// Version is the Server build version.
const Version = "0.1.0"

const shutdownTimeout = 15 * time.Second

func main() {
	if err := run(); err != nil {
		// The logger may not exist yet, so report to stderr and exit non-zero.
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("server failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := logging.New(cfg.LogLevel)
	logger.Info("starting server",
		slog.String("version", Version),
		slog.String("environment", cfg.Environment),
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	startupCtx, cancelStartup := context.WithTimeout(ctx, cfg.StartupTimeout)
	defer cancelStartup()

	stores, err := store.Open(startupCtx, cfg)
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		stores.Close(closeCtx)
	}()
	logger.Info("datastores connected")

	files, err := filestore.New(cfg.RecordingsDir, cfg.PipelinesDir)
	if err != nil {
		return err
	}
	logger.Info("file storage ready")

	repository := store.NewRepository(stores.Postgres)
	sessions := store.NewSessionStore(stores.Redis)
	authService := auth.NewService(repository, sessions, logger, cfg.SessionTTL)

	// spec.md section 17.1: the very first start creates root/toor with a
	// forced password change.
	if err := authService.EnsureBootstrapRoot(startupCtx); err != nil {
		return err
	}

	// spec.md section 11.3: CelesTrak first, SatNOGS as fallback, then the
	// last known-good record already in the database.
	satnogs := tle.NewSatNOGS(cfg.SatNOGSURL, 0)
	providers := tle.NewChain(logger, tle.NewCelestrak(cfg.CelestrakURL, 0), satnogs)
	catalogService := catalog.NewService(repository, providers, satnogs, logger)

	predictionClient, err := prediction.Dial(cfg.PredictionAddress)
	if err != nil {
		return err
	}
	defer func() { _ = predictionClient.Close() }()

	// The prediction service is allowed to start after the Server; readiness
	// reports the truth rather than blocking startup.
	if err := predictionClient.Health(startupCtx); err != nil {
		logger.Warn("prediction service not reachable at startup", slog.String("error", err.Error()))
	} else {
		logger.Info("prediction service reachable")
	}

	planGenerator := passplan.NewGenerator(repository, predictionClient)
	schedulingService := scheduling.NewService(repository, predictionClient, planGenerator, logger)

	apiHandler := httpapi.NewMux(logger, Version, httpapi.Dependencies{
		Files:      files,
		Stores:     stores,
		Repository: repository,
		Auth:       authService,
		Catalog:    catalogService,
		Scheduling: schedulingService,
		Prediction: predictionClient,
	})
	corsOptions := httpapi.CORSOptions{
		AllowedOrigins: cfg.AllowedOrigins,
		AllowLocalhost: cfg.AllowLocalhostOrigins(),
	}
	if corsOptions.Enabled() {
		logger.Info("browser origins allowed",
			slog.Int("configured", len(cfg.AllowedOrigins)),
			slog.Bool("localhost", corsOptions.AllowLocalhost))
	}

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddress,
		Handler:           httpapi.WithCORS(apiHandler, corsOptions),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// TLS covers both listeners or neither. The Worker presents a shared
	// secret on every gRPC call, so an unencrypted link between them would
	// put that credential on the wire (spec.md section 21).
	grpcOptions := []grpc.ServerOption{
		grpc.UnaryInterceptor(workerapi.SharedSecretInterceptor(cfg.WorkerSharedSecret)),
	}
	if cfg.TLSEnabled() {
		certificate, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			return fmt.Errorf("load TLS material: %w", err)
		}
		// TLS 1.2 is the floor: older versions are not worth the ground a
		// station stands on.
		settings := &tls.Config{
			Certificates: []tls.Certificate{certificate},
			MinVersion:   tls.VersionTLS12,
		}
		httpServer.TLSConfig = settings
		grpcOptions = append(grpcOptions, grpc.Creds(credentials.NewTLS(settings)))
		logger.Info("tls enabled for http and grpc")
	} else if cfg.Environment == "production" {
		// Not fatal: a deployment may terminate TLS in front of this process.
		// Silence would be worse than a warning nobody needed.
		logger.Warn("tls is not configured; the worker credential will cross the network in clear " +
			"unless something in front terminates TLS")
	}

	grpcServer := grpc.NewServer(grpcOptions...)
	workerv1.RegisterWorkerServiceServer(grpcServer, workerapi.NewService(repository, files, logger, Version))

	grpcListener, err := net.Listen("tcp", cfg.GRPCAddress)
	if err != nil {
		return err
	}

	// spec.md section 6.3: a station that stops reporting must be shown
	// offline, because scheduling depends on it.
	go workerapi.RunStaleWorkerMonitor(ctx, repository, logger)

	if cfg.TLERefreshEnabled {
		go catalogService.RunRefreshLoop(ctx)
	} else {
		logger.Warn("background tle refresh is disabled")
	}

	errs := make(chan error, 2)

	go func() {
		logger.Info("http listening",
			slog.String("address", cfg.HTTPAddress), slog.Bool("tls", cfg.TLSEnabled()))
		// The certificate is already loaded into TLSConfig, so the paths are
		// deliberately empty here.
		serve := httpServer.ListenAndServe
		if cfg.TLSEnabled() {
			serve = func() error { return httpServer.ListenAndServeTLS("", "") }
		}
		if err := serve(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
	}()

	go func() {
		logger.Info("grpc listening",
			slog.String("address", cfg.GRPCAddress), slog.Bool("tls", cfg.TLSEnabled()))
		if err := grpcServer.Serve(grpcListener); err != nil {
			errs <- err
		}
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
		logger.Info("shutdown requested")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	grpcServer.GracefulStop()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("http shutdown failed", slog.String("error", err.Error()))
	}
	logger.Info("stopped")
	return nil
}
