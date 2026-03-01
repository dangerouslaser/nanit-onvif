# Nanit Stream Proxy

Re-stream your Nanit Baby Monitor's live video and audio feed locally via RTSP, with ONVIF support for direct integration with NVRs like Unifi Protect.

Fork of the original [nanit](https://gitlab.com/adam.stanek/nanit) project.

## Features

- **RTSP server** with H.264 video and AAC audio passthrough
- **ONVIF server** for plug-and-play NVR integration (Unifi Protect, Scrypted, etc.)
- **RTMP server** receives the camera's outbound stream
- **Web dashboard** for authentication, camera status, and sensor monitoring
- **MQTT** publishing of sensor data (temperature, humidity, sound, motion) for Home Assistant / Homebridge
- **Web-based login** with MFA support — no interactive terminal needed

## Quick Start

### 1. Create a `.env` file

```bash
# Your server's LAN IP (must be reachable from the Nanit camera)
NANIT_RTMP_ADDR=192.168.1.88:1935

# ONVIF credentials (for NVR integration)
NANIT_ONVIF_USERNAME=admin
NANIT_ONVIF_PASSWORD=your_password
```

See [.env.sample](.env.sample) for all options.

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
      - "8089:8089"   # ONVIF (NVR discovery)
      - "8080:8080"   # Web UI dashboard
    env_file: .env
    volumes:
      - ./data:/app/data
```

```bash
docker compose up -d
```

### 3. Log in

Open `http://your-server-ip:8080` and log in with your Nanit account credentials. The dashboard will show your camera status and RTSP URL once authenticated.

Alternatively, set `NANIT_REFRESH_TOKEN` in your `.env` file (obtain one via the `-l` interactive login flag).

## Connecting to the Stream

### RTSP (video + audio)

```
rtsp://your-server-ip:8554/local/<baby_uid>
```

The baby UID is shown in the web dashboard and application logs.

### ONVIF (for NVRs)

Point your NVR to the ONVIF service:

```
http://your-server-ip:8089/onvif/device_service
```

Enter the `NANIT_ONVIF_USERNAME` and `NANIT_ONVIF_PASSWORD` you configured. The NVR will automatically discover the RTSP stream with video and audio.

### RTMP

```
rtmp://your-server-ip:1935/local/<baby_uid>
```

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| **General** | | |
| `NANIT_REFRESH_TOKEN` | | Nanit refresh token (alternative to web login) |
| `NANIT_SESSION_FILE` | `data/session.json` | Session persistence file |
| `NANIT_DATA_DIR` | `./data` | Data directory |
| `NANIT_LOG_LEVEL` | `info` | Log level (trace/debug/info/warn/error/fatal) |
| **RTMP** | | |
| `NANIT_RTMP_ENABLED` | `true` | Enable RTMP server |
| `NANIT_RTMP_ADDR` | *(required)* | Public address reachable from camera (e.g. `192.168.1.88:1935`) |
| `NANIT_RTMP_PATH` | `/local` | RTMP path |
| `NANIT_RTMP_KEY` | *(baby UID)* | RTMP stream key |
| **RTSP** | | |
| `NANIT_RTSP_ENABLED` | `true` | Enable RTSP server |
| `NANIT_RTSP_ADDR` | `:8554` | RTSP listen address |
| **ONVIF** | | |
| `NANIT_ONVIF_ENABLED` | `true` | Enable ONVIF server |
| `NANIT_ONVIF_ADDR` | `:8089` | ONVIF listen address |
| `NANIT_ONVIF_USERNAME` | | ONVIF authentication username |
| `NANIT_ONVIF_PASSWORD` | | ONVIF authentication password |
| **Web UI** | | |
| `NANIT_WEB_ENABLED` | `true` | Enable web dashboard |
| `NANIT_WEB_ADDR` | `:8080` | Web UI listen address |
| **MQTT** | | |
| `NANIT_MQTT_ENABLED` | `false` | Enable MQTT sensor publishing |
| `NANIT_MQTT_BROKER_URL` | *(required)* | MQTT broker URL (e.g. `tcp://mosquitto:1883`) |
| `NANIT_MQTT_USERNAME` | | MQTT username |
| `NANIT_MQTT_PASSWORD` | | MQTT password |
| `NANIT_MQTT_CLIENT_ID` | `nanit` | MQTT client ID |
| `NANIT_MQTT_PREFIX` | `nanit` | MQTT topic prefix |
| **Event Polling** | | |
| `NANIT_EVENTS_POLLING` | `false` | Enable sound/motion event polling |
| `NANIT_EVENTS_POLLING_INTERVAL` | `30` | Polling interval in seconds |
| `NANIT_EVENTS_MESSAGE_TIMEOUT` | `300` | Ignore events older than this (seconds) |

## How It Works

The Nanit camera streams video outbound via RTMP when given a destination URL. This application:

1. Tells the camera to push its RTMP stream to this server
2. Receives the H.264 video and AAC audio via RTMP
3. Re-publishes the stream as RTSP with proper RTP packetization
4. Exposes an ONVIF service so NVRs can auto-discover the camera
5. Optionally publishes sensor data (temperature, humidity, motion, sound) to MQTT

Camera communication happens over a WebSocket connection to Nanit's API using Protocol Buffers.

## Setup Guides

- [Home Assistant](./docs/home-assistant.md)
- [Homebridge](./docs/homebridge.md)
- [Sensors](./docs/sensors.md)
- [Docker Compose](./docs/docker-compose.md)

## Development

```bash
go run cmd/nanit/*.go

# Regenerate protobuf (if websocket.proto changes)
protoc --go_out . --go_opt=paths=source_relative pkg/client/websocket.proto

# Run tests
go test ./pkg/...
```

See [Developer Notes](docs/developer-notes.md) for architecture details.

## Known Constraints

- Max 2 concurrent WebSocket connections per camera (Nanit returns 403 if exceeded)
- The camera pushes the stream outbound — it must be able to reach `NANIT_RTMP_ADDR`
- Sound/motion events require REST API polling (not available via WebSocket)

## Disclaimer

This program is for personal and educational use. Use at your own risk and follow any applicable terms when communicating with Nanit servers.

MIT License
