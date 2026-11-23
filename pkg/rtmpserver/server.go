package rtmpserver

import (
	"net"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/notedit/rtmp/av"
	"github.com/notedit/rtmp/codec/h264"
	"github.com/notedit/rtmp/format/rtmp"
	"github.com/rs/zerolog/log"

	"github.com/gregory-m/nanit/pkg/baby"
	"github.com/gregory-m/nanit/pkg/rtspserver"
)

// Opts - options of the RTMP server
type Opts struct {
	// ListenAddr is the address to listen on, e.g. ":1935"
	ListenAddr string

	// Path is the URL path streams are published under, e.g. "/local" for rtmp://host/local/{key}
	Path string

	// ResolveKey maps the stream key (the last segment of the URL) to a baby UID.
	// If nil, the stream key is the baby UID.
	ResolveKey func(streamKey string) (babyUID string, ok bool)
}

type rtmpHandler struct {
	opts              Opts
	babyStateManager  *baby.StateManager
	rtspServer        *rtspserver.RTSPServer
	broadcastersMu    sync.RWMutex
	broadcastersByUID map[string]*broadcaster
}

// rtspBridgeState tracks accumulated config for RTSP stream registration.
type rtspBridgeState struct {
	sps        []byte
	pps        []byte
	aacConfig  []byte
	registered bool
}

// StartRTMPServer - Blocking server. If rtspSrv is non-nil, RTMP packets are bridged to RTSP.
func StartRTMPServer(opts Opts, babyStateManager *baby.StateManager, rtspSrv *rtspserver.RTSPServer) {
	lis, err := net.Listen("tcp", opts.ListenAddr)
	if err != nil {
		log.Fatal().Str("addr", opts.ListenAddr).Err(err).Msg("Unable to start RTMP server")
		panic(err)
	}

	log.Info().Str("addr", opts.ListenAddr).Str("path", opts.Path).Msg("RTMP server started")

	s := rtmp.NewServer()
	s.HandleConn = newRtmpHandler(opts, babyStateManager, rtspSrv).handleConnection

	for {
		nc, err := lis.Accept()
		if err != nil {
			time.Sleep(time.Second)
			continue
		}
		go s.HandleNetConn(nc)
	}
}

func newRtmpHandler(opts Opts, babyStateManager *baby.StateManager, rtspSrv *rtspserver.RTSPServer) *rtmpHandler {
	return &rtmpHandler{
		opts:              opts,
		broadcastersByUID: make(map[string]*broadcaster),
		babyStateManager:  babyStateManager,
		rtspServer:        rtspSrv,
	}
}

var streamKeyRX = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// resolveBabyUID maps the path of an RTMP URL, {Path}/{stream key}, to the baby it belongs to. It has to
// accept exactly the URLs the camera is told to publish to, which are built from the same path and key.
func (s *rtmpHandler) resolveBabyUID(urlPath string) (string, bool) {
	streamKey, ok := strings.CutPrefix(urlPath, strings.TrimRight(s.opts.Path, "/")+"/")
	if !ok || !streamKeyRX.MatchString(streamKey) {
		return "", false
	}

	if s.opts.ResolveKey != nil {
		return s.opts.ResolveKey(streamKey)
	}

	if !baby.IsValidUID(streamKey) {
		return "", false
	}

	return streamKey, true
}

func (s *rtmpHandler) handleConnection(c *rtmp.Conn, nc net.Conn) {
	sublog := log.With().Stringer("client_addr", nc.RemoteAddr()).Logger()

	babyUID, ok := s.resolveBabyUID(c.URL.Path)
	if !ok {
		sublog.Warn().Str("path", c.URL.Path).Msg("Invalid RTMP stream requested")
		nc.Close()
		return
	}

	sublog = sublog.With().Str("baby_uid", babyUID).Logger()

	if c.Publishing {
		sublog.Info().Msg("New stream publisher connected")
		publisher := s.getNewPublisher(babyUID)

		s.babyStateManager.Update(babyUID, *baby.NewState().SetStreamState(baby.StreamState_Alive))

		bridgeState := &rtspBridgeState{}
		for {
			pkt, err := c.ReadPacket()
			if err != nil {
				sublog.Warn().Err(err).Msg("Publisher stream closed unexpectedly")
				s.closePublisher(babyUID, publisher)
				return
			}

			publisher.broadcast(pkt)
			s.bridgeToRTSP(babyUID, pkt, bridgeState)
		}

	} else {
		sublog.Debug().Msg("New stream subscriber connected")
		subscriber, unsubscribe := s.getNewSubscriber(babyUID)

		if subscriber == nil {
			sublog.Warn().Msg("No stream publisher registered yet, closing subscriber stream")
			nc.Close()
			return
		}

		closeC := c.CloseNotify()
		defer unsubscribe()
		for {
			select {
			case pkt := <-subscriber.pktC:
				c.WritePacket(pkt)

			case <-subscriber.done:
				sublog.Debug().Msg("Closing subscriber because publisher quit")
				nc.Close()
				return

			case <-closeC:
				sublog.Debug().Msg("Stream subscriber disconnected")
				nc.Close()
				return
			}
		}
	}
}

func (s *rtmpHandler) getNewPublisher(babyUID string) *broadcaster {
	broadcaster := newBroadcaster()

	s.broadcastersMu.Lock()
	existingBroadcaster, hadExistingBroadcaster := s.broadcastersByUID[babyUID]
	s.broadcastersByUID[babyUID] = broadcaster
	s.broadcastersMu.Unlock()

	if hadExistingBroadcaster {
		log.Warn().Msg("Baby already has active publisher, closing existing subscribers")
		go existingBroadcaster.closeSubscribers()
	}

	return broadcaster
}

func (s *rtmpHandler) getNewSubscriber(babyUID string) (*subscriber, func()) {
	s.broadcastersMu.RLock()
	broadcaster, hasBroadcaster := s.broadcastersByUID[babyUID]
	s.broadcastersMu.RUnlock()

	if !hasBroadcaster {
		return nil, nil
	}

	sub := broadcaster.newSubscriber()

	return sub, func() { broadcaster.unsubscribe(sub) }
}

// closePublisher cleans up after a publisher whose connection ended.
//
// The stream is only torn down (marked unhealthy, RTSP stream unregistered) if b is still the baby's
// current publisher. When the camera reconnects, the connection it replaced can linger for minutes before
// it errors out; by then the stream state and the RTSP stream belong to the new publisher.
func (s *rtmpHandler) closePublisher(babyUID string, b *broadcaster) {
	// The lock is held across the teardown so that a publisher taking over at this moment (getNewPublisher)
	// marks the stream alive and registers its RTSP stream strictly after it, not in the middle.
	s.broadcastersMu.Lock()
	if s.broadcastersByUID[babyUID] == b {
		delete(s.broadcastersByUID, babyUID)

		s.babyStateManager.Update(babyUID, *baby.NewState().SetStreamState(baby.StreamState_Unhealthy))
		if s.rtspServer != nil {
			s.rtspServer.UnregisterStream(babyUID)
		}
	}
	s.broadcastersMu.Unlock()

	b.closeSubscribers()
}

func (s *rtmpHandler) bridgeToRTSP(babyUID string, pkt av.Packet, state *rtspBridgeState) {
	if s.rtspServer == nil {
		return
	}

	switch pkt.Type {
	case av.H264DecoderConfig:
		// Parse SPS/PPS from the decoder configuration record
		data := pkt.VSeqHdr
		if len(data) == 0 {
			data = pkt.Data
		}

		// Also try pre-populated codec on the packet
		var codec *h264.Codec
		var err error
		if pkt.H264 != nil && len(pkt.H264.SPS) > 0 {
			codec = pkt.H264
		} else if len(data) > 0 {
			codec, err = h264.FromDecoderConfig(data)
			if err != nil {
				log.Warn().Err(err).Msg("Failed to parse H264 decoder config for RTSP bridge")
				return
			}
		} else {
			return
		}

		// Extract first SPS and PPS from maps
		var sps, pps []byte
		for _, v := range codec.SPS {
			sps = v
			break
		}
		for _, v := range codec.PPS {
			pps = v
			break
		}

		if len(sps) == 0 || len(pps) == 0 {
			log.Warn().Msg("H264 decoder config missing SPS or PPS for RTSP bridge")
			return
		}

		state.sps = sps
		state.pps = pps
		s.registerRTSPStream(babyUID, state)

	case av.AACDecoderConfig:
		data := pkt.ASeqHdr
		if len(data) == 0 {
			data = pkt.Data
		}
		if len(data) == 0 {
			return
		}
		state.aacConfig = data
		// Re-register with audio if video is already set up
		if len(state.sps) > 0 {
			s.registerRTSPStream(babyUID, state)
		}

	case av.H264:
		if !state.registered {
			return
		}
		nalus, _ := h264.SplitNALUs(pkt.Data)
		if len(nalus) == 0 {
			return
		}
		pts := pkt.Time + pkt.CTime
		s.rtspServer.WriteH264(babyUID, nalus, pts)

	case av.AAC:
		if !state.registered {
			return
		}
		pts := pkt.Time + pkt.CTime
		s.rtspServer.WriteAAC(babyUID, pkt.Data, pts)
	}
}

func (s *rtmpHandler) registerRTSPStream(babyUID string, state *rtspBridgeState) {
	if err := s.rtspServer.RegisterStream(babyUID, state.sps, state.pps, state.aacConfig); err != nil {
		log.Warn().Err(err).Msg("Failed to register RTSP stream")
		return
	}
	state.registered = true
}
