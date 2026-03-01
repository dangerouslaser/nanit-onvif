package rtmpserver

import (
	"net"
	"regexp"
	"sync"
	"time"

	"github.com/notedit/rtmp/av"
	"github.com/notedit/rtmp/codec/h264"
	"github.com/notedit/rtmp/format/rtmp"
	"github.com/rs/zerolog/log"

	"github.com/gregory-m/nanit/pkg/baby"
	"github.com/gregory-m/nanit/pkg/rtspserver"
)

type rtmpHandler struct {
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
func StartRTMPServer(addr string, babyStateManager *baby.StateManager, rtspSrv *rtspserver.RTSPServer) {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal().Str("addr", addr).Err(err).Msg("Unable to start RTMP server")
		panic(err)
	}

	log.Info().Str("addr", addr).Msg("RTMP server started")

	s := rtmp.NewServer()
	s.HandleConn = newRtmpHandler(babyStateManager, rtspSrv).handleConnection

	for {
		nc, err := lis.Accept()
		if err != nil {
			time.Sleep(time.Second)
			continue
		}
		go s.HandleNetConn(nc)
	}
}

func newRtmpHandler(babyStateManager *baby.StateManager, rtspSrv *rtspserver.RTSPServer) *rtmpHandler {
	return &rtmpHandler{
		broadcastersByUID: make(map[string]*broadcaster),
		babyStateManager:  babyStateManager,
		rtspServer:        rtspSrv,
	}
}

var rtmpURLRX = regexp.MustCompile(`^/local/([a-z0-9_-]+)$`)

func (s *rtmpHandler) handleConnection(c *rtmp.Conn, nc net.Conn) {
	sublog := log.With().Stringer("client_addr", nc.RemoteAddr()).Logger()

	submatch := rtmpURLRX.FindStringSubmatch(c.URL.Path)
	if len(submatch) != 2 {
		sublog.Warn().Str("path", c.URL.Path).Msg("Invalid RTMP stream requested")
		nc.Close()
		return
	}

	babyUID := submatch[1]
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
				s.babyStateManager.Update(babyUID, *baby.NewState().SetStreamState(baby.StreamState_Unhealthy))
				s.closePublisher(babyUID, publisher)
				if s.rtspServer != nil {
					s.rtspServer.UnregisterStream(babyUID)
				}
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
		for {
			select {
			case pkt, open := <-subscriber.pktC:
				if !open {
					sublog.Debug().Msg("Closing subscriber because publisher quit")
					nc.Close()
					return
				}

				c.WritePacket(pkt)

			case <-closeC:
				sublog.Debug().Msg("Stream subscriber disconnected")
				unsubscribe()
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

func (s *rtmpHandler) closePublisher(babyUID string, b *broadcaster) {
	s.broadcastersMu.Lock()
	if currBroadcaster, hasExistingBroadcaster := s.broadcastersByUID[babyUID]; hasExistingBroadcaster {
		if currBroadcaster == b {
			delete(s.broadcastersByUID, babyUID)
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
