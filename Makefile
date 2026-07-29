# Variables
BINARY_DIR  = "bin"
BINARY_NAME = "makedog"

SRCS := $(shell find src -name '*.go')

all: build

build: $(BINARY_DIR)/$(BINARY_NAME)

$(BINARY_DIR)/$(BINARY_NAME): $(SRCS) go.mod go.sum
	go build -trimpath -o $(BINARY_DIR)/$(BINARY_NAME) ./src

build-linux: $(BINARY_DIR)/$(BINARY_NAME)-linux

$(BINARY_DIR)/$(BINARY_NAME)-linux: $(SRCS) go.mod go.sum
	GOOS=linux GOARCH=amd64 go build -trimpath -o $(BINARY_DIR)/$(BINARY_NAME)-linux ./src

fmt:
	@go fmt ./src/...

clean:
	rm -rf $(BINARY_DIR)/

test:
	@go test -v ./src/...

# Demo fixtures

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

demo-custom-signals: build fixture-sigecho
	@$(BINARY_DIR)/$(BINARY_NAME) --config test/fixtures/makedog.toml-custom-signals $(BINARY_DIR)/test-sigecho

.PHONY: all build build-linux fmt clean run test demo-pulse demo-sigecho demo-spin demo-custom-signals
