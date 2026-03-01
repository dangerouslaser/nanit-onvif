# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Nanit Stream Proxy — a Go application that re-streams Nanit Baby Monitor live feeds via a local RTMP server and publishes sensor data (temperature, humidity, sound, motion) to MQTT for home automation integration (Home Assistant, Homebridge).

## Build & Test Commands

```bash
# Build
go build -v ./...

# Run all tests
go test -v ./...

# Run tests for a specific package
go test -v ./pkg/baby/
go test -v ./pkg/utils/

# Run the application (interactive login mode)
go run ./cmd/nanit -l

# Run the application (normal mode, requires prior login session)
go run ./cmd/nanit

# Regenerate protobuf (if websocket.proto changes)
protoc --go_out=. pkg/client/websocket.proto
```

## Architecture

**Entry point**: `cmd/nanit/main.go` — parses CLI flags and env vars, handles interactive login (`-l` flag), creates and runs the App.

**Core packages** under `pkg/`:

- **app** — Main orchestrator. `App.Run()` authorizes with Nanit API, fetches baby list, starts RTMP/MQTT services, then spawns per-baby goroutines that manage WebSocket connections and stream health.
- **client** — Nanit API client. `rest.go` handles REST auth (token refresh, MFA). `websocket.go` manages protobuf-encoded WebSocket connections to Nanit servers for camera control and sensor data.
- **rtmpserver** — Embedded RTMP server with pub/sub model. Camera publishes stream, local clients subscribe. Stream health monitoring tracks alive/unhealthy states.
- **mqtt** — Publishes baby sensor state changes to an MQTT broker under `nanit/babies/{uid}/{sensor}` topics. Auto-reconnects with exponential backoff.
- **baby** — Baby data structures and thread-safe state management with observable pub/sub pattern. State merging logic tracks stream status, sensors, and WebSocket connectivity.
- **session** — JSON file persistence for auth tokens and baby list across restarts. Versioned schema (revision 3).
- **message** — Event message structures and constants for sound/motion detection.
- **utils** — Shared utilities: env var parsing, graceful shutdown via context, retry with exponential backoff, file tailing.

**Communication protocol**: WebSocket messages use Protocol Buffers v2 (`pkg/client/websocket.proto` → `websocket.pb.go`). The camera streams video via RTMP (outbound push from camera to the local RTMP server).

## Configuration

All configuration via environment variables (see `.env.sample` for full list). Key prefixes: `NANIT_RTMP_*`, `NANIT_MQTT_*`, `NANIT_EVENTS_*`. Session/data stored in `NANIT_DATA_DIR` (default `./data`).

## Docker

Multi-stage Dockerfile: builds with `golang:1.21`, runs on Alpine. CI builds multi-platform images (amd64, arm64) to `ghcr.io/gregory-m/nanit`. Persistent volume at `/app/data`.

## Key Patterns

- **Graceful shutdown**: `utils.GracefulContext` and `utils.RunWithGracefulCancel()` for hierarchical context cancellation on interrupt signals.
- **Retry logic**: `utils.Attempter` provides exponential backoff (used for MQTT reconnection, API calls).
- **State pub/sub**: `baby.StateManager` notifies subscribers on state changes; used by MQTT and RTMP components to react to sensor/stream updates.
- **Logging**: zerolog with console writer. Token values are partially masked in logs.

## Known Constraints

- Max 2 concurrent WebSocket connections per camera device (403 if exceeded).
- Local streaming is outbound-only: the camera pushes to an RTMP URL you provide.
- Sound/motion events require polling the REST API (not available via WebSocket).
