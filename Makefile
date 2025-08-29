# Makefile — build a Go app for multiple OS/ARCH combos
# -----------------------------------------------------
# Defaults (override on the CLI, e.g. `make APP=myapp MAIN=./cmd/myapp build-all`)
APP				 ?= $(strip $(notdir $(CURDIR)))
MAIN       ?= main.go
OUTDIR     ?= dist
CGO_ENABLED?= 0
GO         ?= go

# Keep ldflags minimal so this works without requiring variables in your code.
# If you want version info, pass LDFLAGS on the CLI with -X flags.
LDFLAGS    ?= -s -w

# Host detection for native build
HOST_OS    := $(shell $(GO) env GOOS)
HOST_ARCH  := $(shell $(GO) env GOARCH)
ifeq ($(HOST_OS),windows)
EXEEXT := .exe
else
EXEEXT :=
endif

# Platforms built by `make build-all` (edit as needed)
PLATFORMS ?= \
	linux/amd64 \
	linux/arm64 \
	darwin/amd64 \
	darwin/arm64 \
	windows/amd64 \
	windows/arm64

# -----------------------------------------------------
# Targets
# -----------------------------------------------------
.PHONY: help build build-one build-all clean tree checksums

help:
	@echo "Usage:"
	@echo "  make build                          # build for host ($(HOST_OS)/$(HOST_ARCH))"
	@echo "  make build-one OS=<os> ARCH=<arch>  # cross-compile one target"
	@echo "  make build-all                      # build for PLATFORMS ($(PLATFORMS))"
	@echo "  make checksums                      # write SHA256SUMS.txt for artifacts"
	@echo "  make clean                          # remove $(OUTDIR)"
	@echo ""
	@echo "Vars you can override: APP, MAIN, OUTDIR, CGO_ENABLED, LDFLAGS, PLATFORMS"
	@echo "Examples:"
	@echo "  make build-one OS=linux ARCH=arm64"
	@echo "  make LDFLAGS=\"-s -w -X 'example.com/project/buildinfo.Version=v1.2.3'\" build"

# Native build (host OS/ARCH)
build:
	@mkdir -p $(OUTDIR)
	@echo ">> Building $(APP) for $(HOST_OS)/$(HOST_ARCH)"
	@CGO_ENABLED=$(CGO_ENABLED) $(GO) build -trimpath -ldflags "$(LDFLAGS)" \
		-o $(OUTDIR)/$(APP)$(EXEEXT) $(MAIN)

# Cross-compile one OS/ARCH (defaults to host if not provided)
OS   ?= $(HOST_OS)
ARCH ?= $(HOST_ARCH)

build-one:
	@mkdir -p $(OUTDIR)
	echo ">> Building $(APP) for $(OS)/$(ARCH)"; \
	echo "$(OUTDIR)/$(APP)-$(OS)"
	GOOS=$(OS) GOARCH=$(ARCH) CGO_ENABLED=$(CGO_ENABLED) \
		$(GO) build -trimpath -ldflags "$(LDFLAGS)" \
		-o $(OUTDIR)/$(APP)-$(OS)-$(ARCH) $(MAIN)

# Bulk cross-compile using the PLATFORMS list
build-all: $(PLATFORMS:%=build/%)

# Pattern rule backing build-all
build/%:
	@os=$$(echo "$*" | cut -d/ -f1); \
	arch=$$(echo "$*" | cut -d/ -f2); \
	ext=""; [ "$$os" = "windows" ] && ext=".exe" || true; \
	echo ">> Building $(APP) for $$os/$$arch"; \
	mkdir -p $(OUTDIR); \
	GOOS=$$os GOARCH=$$arch CGO_ENABLED=$(CGO_ENABLED) \
		$(GO) build -trimpath -ldflags "$(LDFLAGS)" \
		-o $(OUTDIR)/$(APP)-$$os-$$arch$$ext $(MAIN)

# Generate SHA-256 checksums for all artifacts
checksums:
	@command -v shasum >/dev/null 2>&1 && algo="shasum -a 256" || algo="sha256sum"; \
	if [ -d "$(OUTDIR)" ]; then \
	  echo ">> Generating SHA-256 checksums for $(OUTDIR)"; \
	  $$algo $(OUTDIR)/* > $(OUTDIR)/SHA256SUMS.txt; \
	  echo "   Wrote $(OUTDIR)/SHA256SUMS.txt"; \
	else \
	  echo "No $(OUTDIR) directory; run a build first."; \
	fi

clean:
	@echo ">> Cleaning $(OUTDIR)"
	@rm -rf $(OUTDIR)

# Quick look at artifacts
tree:
	@echo ">> Artifacts in $(OUTDIR)"; \
	[ -d "$(OUTDIR)" ] && ls -lh $(OUTDIR) || echo "(none)"
