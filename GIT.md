# Git Workflow for plugin-kasa

This repository contains the Slidebolt Kasa Plugin, providing integration with TP-Link Kasa smart plugs and switches. It produces a standalone binary.

## Dependencies
- **Internal:**
  - `sb-contract`: Core interfaces.
  - `sb-domain`: Shared domain models.
  - `sb-messenger-sdk`: Shared messaging interfaces.
  - `sb-runtime`: Core execution environment.
  - `sb-storage-sdk`: Shared storage interfaces.
  - `sb-testkit`: Testing utilities.
- **External:** 
  - Standard Go library and NATS.

## Build Process
- **Type:** Go Application (Plugin).
- **Consumption:** Run as a background plugin service.
- **Artifacts:** Produces a binary named `plugin-kasa`.
- **Command:** `go build -o plugin-kasa ./cmd/plugin-kasa`
- **Validation:** 
  - Validated through unit tests: `go test -v ./...`
  - Validated by successful compilation of the binary.

## Pre-requisites & Publishing
As a Kasa integration plugin, `plugin-kasa` must be updated whenever the core domain, messaging, storage, or testkit SDKs are changed.

**Before publishing:**
1. Determine current tag: `git tag | sort -V | tail -n 1`
2. Ensure all local tests pass: `go test -v ./...`
3. Ensure the binary builds: `go build -o plugin-kasa ./cmd/plugin-kasa`

**Publishing Order:**
1. Ensure all internal dependencies are tagged and pushed.
2. Update `plugin-kasa/go.mod` to reference the latest tags.
3. Determine next semantic version for `plugin-kasa` (e.g., `v1.0.5`).
4. Commit and push the changes to `main`.
5. Tag the repository: `git tag v1.0.5`.
6. Push the tag: `git push origin main v1.0.5`.

## Update Workflow & Verification
1. **Modify:** Update Kasa integration logic in `app/` or translation logic in `internal/`.
2. **Verify Local:**
   - Run `go mod tidy`.
   - Run `go test ./...`.
   - Run `go build -o plugin-kasa ./cmd/plugin-kasa`.
3. **Commit:** Ensure the commit message clearly describes the Kasa plugin change.
4. **Tag & Push:** (Follow the Publishing Order above).
