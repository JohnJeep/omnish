# omnish Makefile
# Usage:
#   make build         # build for the current platform
#   make all           # build for all platforms
#   make test          # run tests
#   make clean         # remove build artifacts
#   make run           # run locally (stdio shell)

BINARY   := omnish
VERSION  := v0.1.0
LDFLAGS  := -s -w -X main.buildVersion=$(VERSION)
BIN      := bin
CMD      := ./cmd/omnish

# CGO must be disabled to produce a static binary
export CGO_ENABLED=0
export GOPRIVATE=*

.PHONY: build
build:
	go build -ldflags="$(LDFLAGS)" -o $(BINARY) $(CMD)
	@echo "Built: $(BINARY)"

# Cross-compilation matrix for all platforms
PLATFORMS := \
  linux/amd64 \
  linux/arm64 \
  darwin/amd64 \
  darwin/arm64 \
  windows/amd64 \
  windows/arm64

.PHONY: all
all: $(PLATFORMS)

$(PLATFORMS):
	$(eval OS   := $(word 1,$(subst /, ,$@)))
	$(eval ARCH := $(word 2,$(subst /, ,$@)))
	$(eval EXT  := $(if $(filter windows,$(OS)),.exe,))
	GOOS=$(OS) GOARCH=$(ARCH) go build \
	  -ldflags="$(LDFLAGS)" \
	  -o $(BIN)/$(BINARY)-$(OS)-$(ARCH)$(EXT) \
	  $(CMD)
	@echo "Built: $(BIN)/$(BINARY)-$(OS)-$(ARCH)$(EXT)"

.PHONY: test
test:
	go test ./... -count=1

.PHONY: test-verbose
test-verbose:
	go test ./... -v -count=1

.PHONY: vet
vet:
	go vet ./...

.PHONY: run
run:
	go run $(CMD) serve --log debug

.PHONY: clean
clean:
	rm -rf $(BIN)/

.PHONY: help
help:
	@echo "Targets:"
	@echo "  default       build for current platform"
	@echo "  all           build for all platforms (linux/darwin/windows × amd64/arm64)"
	@echo "  test          run all tests"
	@echo "  test-verbose  run tests with verbose output"
	@echo "  vet           run go vet"
	@echo "  run           run locally (stdio shell)"
	@echo "  clean         remove build artifacts"
