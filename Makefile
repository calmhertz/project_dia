# Aagasa developer entrypoints.
# Go and Flutter may live outside the default PATH; see docs/architecture-baseline.md.

.PHONY: help deps deps-check proto build test test-integration integration-test drills acceptance env-up env-down env-reset env-status migrate fmt lint clean

help:
	@echo "deps    - install everything needed (Ubuntu/Debian or Fedora)"
	@echo "deps-check - report what is missing, install nothing"
	@echo "proto   - regenerate protobuf/gRPC code for Go and Python"
	@echo "build   - build every component"
	@echo "test    - test every component (no databases needed)"
	@echo "test-integration - run store tests against real databases"
	@echo "env-up  - bring up a complete environment with simulated hardware"
	@echo "env-down    - stop the services, keep the data"
	@echo "env-reset   - stop everything and delete the data"
	@echo "env-status  - what is running"
	@echo "integration-test - drive the whole API against a running environment"
	@echo "drills  - take the running system apart and check it recovers"
	@echo "acceptance - run the V1 acceptance scenarios (plan.md V19)"
	@echo "migrate - apply the PostgreSQL schema and MongoDB collections"
	@echo "fmt     - format every component"
	@echo "lint    - static analysis for every component"
	@echo "clean   - remove build output and generated code"

deps:
	scripts/install-dependencies.sh

deps-check:
	scripts/install-dependencies.sh --check

# Generated code is not committed; run this after cloning or changing a .proto.
proto:
	mkdir -p server/go/internal/gen
	protoc -I proto \
	  --go_out=server/go/internal/gen --go_opt=module=aagasa/internal/gen \
	  --go-grpc_out=server/go/internal/gen --go-grpc_opt=module=aagasa/internal/gen \
	  proto/aagasa/prediction/v1/prediction.proto \
	  proto/aagasa/worker/v1/worker.proto proto/aagasa/worker/v1/passplan.proto \
	  proto/aagasa/worker/v1/recording.proto
	cd server/python && uv run python -m grpc_tools.protoc -I ../../proto \
	  --python_out=src --grpc_python_out=src --pyi_out=src \
	  ../../proto/aagasa/prediction/v1/prediction.proto
	cd worker && uv run python -m grpc_tools.protoc -I ../proto \
	  --python_out=src --grpc_python_out=src --pyi_out=src \
	  ../proto/aagasa/worker/v1/worker.proto ../proto/aagasa/worker/v1/passplan.proto \
	  ../proto/aagasa/worker/v1/recording.proto

build:
	cd server/go && go build ./...
	cd server/python && uv sync
	cd worker && uv sync
	cd client && flutter build web --release

test:
	cd server/go && go test ./...
	cd server/python && uv run pytest -q
	cd worker && uv run pytest -q
	cd client && flutter test

# Requires AAGASA_TEST_POSTGRES_URL, AAGASA_TEST_REDIS_URL and
# AAGASA_TEST_MONGO_URL. Without them the store tests skip.
test-integration:
	cd server/go && go test ./internal/store/... -count=1

# A complete environment, no hardware required (plan.md V17).
# AAGASA_HARDWARE=real uses the attached rotator and dongle instead.
env-up:
	scripts/dev-env.sh up

env-down:
	scripts/dev-env.sh down

env-reset:
	scripts/dev-env.sh reset

env-status:
	scripts/dev-env.sh status

# Both drive a running environment rather than starting one.
integration-test:
	scripts/integration-test.sh

drills:
	scripts/failure-drills.sh

# Give this an environment of its own: it stops and starts services.
acceptance:
	scripts/acceptance.sh

# Requires AAGASA_POSTGRES_URL and AAGASA_MONGO_URL.
migrate:
	cd server/go && go run ./cmd/aagasa-migrate up
	cd server/go && go run ./cmd/aagasa-migrate mongo-init

fmt:
	cd server/go && gofmt -w .
	cd client && dart format lib test

lint:
	cd server/go && go vet ./...
	cd client && flutter analyze

clean:
	rm -rf server/go/internal/gen server/python/src/aagasa worker/src/aagasa
	rm -rf client/build
