# Variables
BINARY_DIR  = "bin"
BINARY_NAME = "makedog"

# Discoverables
VERSION?   = dev
COMMIT     = $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME = $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS    = -ldflags "-X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.BuildTime=$(BUILD_TIME)"

SRCS := $(shell find src -name '*.go')

all: build

build: $(BINARY_DIR)/$(BINARY_NAME)

$(BINARY_DIR)/$(BINARY_NAME): $(SRCS) go.mod go.sum
	@go build $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME) ./src

build-linux: $(BINARY_DIR)/$(BINARY_NAME)-linux

$(BINARY_DIR)/$(BINARY_NAME)-linux: $(SRCS) go.mod go.sum
	@GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME)-linux ./src

fmt:
	@go fmt ./src/...

clean:
	rm -rf $(BINARY_DIR)/

run: build
	@makedog $(BINARY_DIR)/$(BINARY_NAME)

test:
	@go test -v ./src/...

test-short:
	@go test -short -v ./src/...

fixture-pulse:
	@go build -o $(BINARY_DIR)/test-pulse test/fixtures/test-pulse.go

fixture-sigecho:
	@go build -o $(BINARY_DIR)/test-sigecho test/fixtures/test-sigecho.go

fixture-spin:
	@go build -o $(BINARY_DIR)/test-spin test/fixtures/test-spin.go

demo-pulse: build fixture-pulse
	@$(BINARY_DIR)/$(BINARY_NAME) $(BINARY_DIR)/test-pulse

demo-sigecho: build fixture-sigecho
	@$(BINARY_DIR)/$(BINARY_NAME) $(BINARY_DIR)/test-sigecho

demo-spin: build fixture-spin
	@$(BINARY_DIR)/$(BINARY_NAME) $(BINARY_DIR)/test-spin

run-claude: build
	$(BINARY_DIR)/$(BINARY_NAME)

deploy: build-linux
	@scripts/deploy-server

migrations: build
	@$(BINARY_DIR)/$(BINARY_NAME) --migrate

migrate-forward: build
	@$(BINARY_DIR)/$(BINARY_NAME) --migrate-forward

migrate-backward: build
	@$(BINARY_DIR)/$(BINARY_NAME) --migrate-backward

new-migration: build
	@$(BINARY_DIR)/$(BINARY_NAME) --new-migration "$(label)"

.PHONY: all build build-linux fmt clean run test test-short fixture-pulse fixture-sigecho fixture-spin demo-pulse demo-sigecho demo-spin migrate migrate-forward migrate-backward new-migration
