# Aagasa build and maintenance entrypoints.
# Go and Flutter may live outside the default PATH; see docs/architecture-baseline.md.
#
# Deployment is ./deploy.sh (server | worker | both | down); build the web
# client here first with `make build`.

.PHONY: help deps deps-check proto build test migrate fmt lint clean

help:
	@echo "deps    - install everything needed (Ubuntu/Debian or Fedora)"
	@echo "deps-check - report what is missing, install nothing"
	@echo "proto   - regenerate protobuf/gRPC code for Go and Python"
	@echo "build   - build every component"
	@echo "test    - run every component's unit tests (no databases or hardware needed)"
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
