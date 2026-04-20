# Nanit Stream Proxy

Re-stream your Nanit Baby Monitor's live video and audio locally via RTSP, with full ONVIF support (camera, snapshot, motion/sound events) and MQTT with Home Assistant auto-discovery. Lets any standard NVR (Unifi Protect, Frigate, Blue Iris, Scrypted) treat the Nanit like a normal IP camera, and surfaces every sensor and control the camera exposes to Home Assistant as a single device.

Fork of the original [nanit](https://gitlab.com/adam.stanek/nanit) project.

## Features

- **RTSP server** — H.264 video + AAC audio passthrough over standard RTSP
- **ONVIF server** — profile discovery, stream URI, snapshots, and **PullPoint events** for motion and sound (so NVRs get real motion/audio triggers, not just a dumb stream)
- **ONVIF snapshots** — `/onvif/snapshot/<uid>.jpg`, produced from the last RTSP keyframe via ffmpeg (no second stream required)
- **MQTT publishing** with **Home Assistant MQTT discovery** — one HA device per baby, ~17 sensors and controls auto-created (temperature, humidity, light level, motion, sound, firmware, mounting mode, sleep mode, night light, status light, mic mute, volume, night vision, etc.)
- **MQTT commands** — publish to `set/{field}` topics to flip night light, toggle mic, change volume, etc.; in HA these render as switches and numbers you can actuate
- **Unified HA device** — the ONVIF camera and MQTT entities auto-merge into a single device card via a shared synthetic MAC, so camera + snapshot + sensors + switches all live together
- **Embedded RTMP ingest** — receives the camera's outbound stream; no external RTMP server or go2rtc needed
- **Web dashboard** — login (including MFA), camera status, RTSP URLs, live sensor values, no terminal required

## Quick Start

### 1. Create a `.env` file

```bash
# LAN IP of this server, reachable from the Nanit camera
NANIT_RTMP_ADDR=192.168.1.88:1935

# Optional: ONVIF auth (recommended if exposed beyond the host)
NANIT_ONVIF_USERNAME=admin
NANIT_ONVIF_PASSWORD=your_password

# Optional: MQTT → Home Assistant
NANIT_MQTT_ENABLED=true
NANIT_MQTT_BROKER_URL=tcp://192.168.1.88:1883
NANIT_MQTT_HA_DISCOVERY=true
NANIT_MQTT_COMMANDS=true

# Optional but recommended: enables motion/sound events (REST-polled)
NANIT_EVENTS_POLLING=true
NANIT_EVENTS_POLLING_INTERVAL=10
```

Full list in [.env.sample](.env.sample).

### 2. Start with Docker Compose

```yaml
services:
  nanit:
    container_name: nanit
    build: .
    restart: unless-stopped
    ports:
      - "1935:1935"   # RTMP (camera push)
      - "8554:8554"   # RTSP (video output)
      - "8089:8089"   # ONVIF + snapshots + events
      - "8080:8080"   # Web UI
    env_file: .env
    volumes:
      - ./data:/app/data
```

```bash
docker compose up -d
```

### 3. Log in

Open `http://your-server-ip:8080` and sign in with your Nanit account (MFA supported). The dashboard shows camera status, baby UID, and the stream URLs once authenticated. Alternatively set `NANIT_REFRESH_TOKEN` (obtain via `go run ./cmd/nanit -l`).

## Connecting to the stream

### RTSP

```
rtsp://your-server-ip:8554/local/<baby_uid>
```

### ONVIF (recommended for NVRs and Home Assistant)

Point your NVR or HA at the ONVIF device:

```
http://your-server-ip:8089
```

Enter the ONVIF username/password if you set them. The NVR discovers the RTSP stream, snapshot URL, and (for clients that use PullPoint subscriptions) motion and sound events as `tns1:RuleEngine/CellMotionDetector/Motion` and `tns1:AudioAnalytics/Audio/DetectedSound`.

### RTMP (legacy)

```
rtmp://your-server-ip:1935/local/<baby_uid>
```

## Home Assistant

1. **ONVIF integration** — Settings → Devices & Services → Add → **ONVIF Device** → `your-server-ip:8089`. Creates the camera entity, snapshot, and motion/sound binary_sensors.
2. **MQTT integration** — already connected to your broker, make sure discovery is enabled. With `NANIT_MQTT_HA_DISCOVERY=true`, nanit publishes retained discovery configs that auto-create temperature, humidity, motion, sound, volume, firmware, night light, etc.
3. **Merge** — nanit reports the same synthetic MAC on both sides, so HA automatically merges the ONVIF device and the MQTT device into one device card.
4. **Commands** — with `NANIT_MQTT_COMMANDS=true`, writable fields (night light, volume, mic mute, sleep mode, status light, night vision) publish as switches and numbers you can actuate from HA.

## Environment variables

| Variable | Default | Description |
|---|---|---|
| **General** | | |
| `NANIT_REFRESH_TOKEN` | | Nanit refresh token (alternative to web login) |
| `NANIT_SESSION_FILE` | `data/session.json` | Session persistence file |
| `NANIT_LOG_LEVEL` | `info` | `trace`/`debug`/`info`/`warn`/`error`/`fatal` |
| **RTMP** | | |
| `NANIT_RTMP_ENABLED` | `true` | Enable embedded RTMP server |
| `NANIT_RTMP_ADDR` | *(required)* | Public IP:port reachable from camera |
| `NANIT_RTMP_PATH` | `/local` | RTMP path |
| `NANIT_RTMP_KEY` | *(baby UID)* | RTMP stream key |
| **RTSP** | | |
| `NANIT_RTSP_ENABLED` | `true` | Enable embedded RTSP server |
| `NANIT_RTSP_ADDR` | `:8554` | RTSP listen address |
| **ONVIF** | | |
| `NANIT_ONVIF_ENABLED` | `true` | Enable ONVIF server |
| `NANIT_ONVIF_ADDR` | `:8089` | ONVIF listen address |
| `NANIT_ONVIF_USERNAME` | | WS-Security username |
| `NANIT_ONVIF_PASSWORD` | | WS-Security password |
| `NANIT_ONVIF_EVENTS` | `true` | Enable PullPoint events (motion/sound) |
| `NANIT_ONVIF_EVENT_HOLD` | `30` | How long (sec) motion/sound stays active after a REST-polled event |
| **Web UI** | | |
| `NANIT_WEB_ENABLED` | `true` | Enable web dashboard |
| `NANIT_WEB_ADDR` | `:8080` | Web UI listen address |
| **MQTT** | | |
| `NANIT_MQTT_ENABLED` | `false` | Enable MQTT publishing |
| `NANIT_MQTT_BROKER_URL` | *(required)* | e.g. `tcp://broker:1883` |
| `NANIT_MQTT_USERNAME` | | MQTT username |
| `NANIT_MQTT_PASSWORD` | | MQTT password |
| `NANIT_MQTT_CLIENT_ID` | `nanit` | Client ID |
| `NANIT_MQTT_PREFIX` | `nanit` | Topic prefix; topics are `<prefix>/babies/<uid>/<key>` |
| `NANIT_MQTT_HA_DISCOVERY` | `false` | Publish Home Assistant MQTT discovery topics |
| `NANIT_MQTT_HA_DISCOVERY_PREFIX` | `homeassistant` | HA discovery topic prefix |
| `NANIT_MQTT_COMMANDS` | `false` | Subscribe to `<prefix>/babies/<uid>/set/<field>` to control the camera |
| **Event Polling** | | |
| `NANIT_EVENTS_POLLING` | `false` | Poll the Nanit REST API for motion/sound events |
| `NANIT_EVENTS_POLLING_INTERVAL` | `30` | Poll interval (seconds) |
| `NANIT_EVENTS_MESSAGE_TIMEOUT` | `300` | Ignore events older than this (seconds) |
| `NANIT_EVENTS_DETECTED_HOLD` | `30` | How long `motion_detected` / `sound_detected` stay true after an event |

## MQTT topics

State (retained, QoS 0):

```
<prefix>/babies/<uid>/temperature         float, °C
<prefix>/babies/<uid>/humidity            float, %
<prefix>/babies/<uid>/light_level         integer
<prefix>/babies/<uid>/motion_detected     true | false
<prefix>/babies/<uid>/sound_detected      true | false
<prefix>/babies/<uid>/motion_timestamp    Unix seconds of last event
<prefix>/babies/<uid>/sound_timestamp     Unix seconds of last event
<prefix>/babies/<uid>/is_night            true | false
<prefix>/babies/<uid>/night_light_on      true | false
<prefix>/babies/<uid>/night_light_timeout seconds
<prefix>/babies/<uid>/night_vision        true | false
<prefix>/babies/<uid>/sleep_mode          true | false
<prefix>/babies/<uid>/status_light_on     true | false
<prefix>/babies/<uid>/mic_mute_on         true | false
<prefix>/babies/<uid>/volume              0..100
<prefix>/babies/<uid>/mounting_mode       enum
<prefix>/babies/<uid>/firmware_version    string
<prefix>/babies/<uid>/hardware_version    string
<prefix>/babies/<uid>/is_stream_alive     true | false
<prefix>/babies/<uid>/is_websocket_alive  true | false
```

Commands (when `NANIT_MQTT_COMMANDS=true`):

```
<prefix>/babies/<uid>/set/night_light_on       true | false | ON | OFF
<prefix>/babies/<uid>/set/night_light_timeout  seconds
<prefix>/babies/<uid>/set/night_vision         true | false
<prefix>/babies/<uid>/set/sleep_mode           true | false
<prefix>/babies/<uid>/set/status_light_on      true | false
<prefix>/babies/<uid>/set/mic_mute_on          true | false
<prefix>/babies/<uid>/set/volume               0..100
```

## How it works

1. The camera streams video outbound via RTMP when given a destination URL.
2. Nanit's WebSocket API (Protocol Buffers) is used to tell the camera to push to this server, to read environmental sensors, and to read/write settings and control state.
3. RTMP frames are demuxed and re-packetized as RTP, then served over RTSP.
4. ONVIF SOAP endpoints advertise the RTSP URL, snapshot URL, and (optionally) a PullPoint events service backed by the same state updates that feed MQTT.
5. Motion and sound are polled from the Nanit REST `/babies/<uid>/messages` endpoint (not available over WebSocket), held for a configurable window, then cleared.

## Setup guides

- [Home Assistant](./docs/home-assistant.md)
- [Homebridge](./docs/homebridge.md)
- [Sensors](./docs/sensors.md)
- [Docker Compose](./docs/docker-compose.md)
- [Developer Notes](./docs/developer-notes.md)

## Development

```bash
go build ./...
go test ./...

# Regenerate protobuf (if websocket.proto changes)
protoc --go_out . --go_opt=paths=source_relative pkg/client/websocket.proto

# Run in place
go run ./cmd/nanit       # needs a refresh token or prior session
go run ./cmd/nanit -l    # interactive login
```

## Known constraints

- Max 2 concurrent WebSocket connections per camera (Nanit returns 403 if exceeded).
- Camera pushes the stream outbound — it must be able to reach `NANIT_RTMP_ADDR`.
- Motion and sound events come from REST polling; latency is bounded by `NANIT_EVENTS_POLLING_INTERVAL`. Nanit does not push these over WebSocket.
- ONVIF WS-Discovery (network autodiscovery) is not implemented. Add the device manually by host:port in HA / NVR.

## Credits

- [gregory-m/nanit](https://github.com/gregory-m/nanit) — Original project this fork is based on. Core Nanit API client, WebSocket communication, RTMP streaming, MQTT integration, and session management.
- [AlexxIT/go2rtc](https://github.com/AlexxIT/go2rtc) — ONVIF SOAP response templates and XML helpers adapted from go2rtc (MIT License).

## Disclaimer

For personal and educational use. Use at your own risk and follow any applicable terms when communicating with Nanit servers.

MIT License
