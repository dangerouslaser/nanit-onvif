package rtspserver

import (
	"regexp"
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph264"
	"github.com/rs/zerolog/log"
)

type streamEntry struct {
	stream *gortsplib.ServerStream
	media  *description.Media
	forma  *format.H264
	enc    *rtph264.Encoder
}

// RTSPServer wraps a gortsplib RTSP server and manages per-baby streams.
type RTSPServer struct {
	server  *gortsplib.Server
	mu      sync.RWMutex
	streams map[string]*streamEntry // keyed by babyUID
}

// NewRTSPServer creates a new RTSP server listening on the given address.
func NewRTSPServer(addr string) *RTSPServer {
	s := &RTSPServer{
		streams: make(map[string]*streamEntry),
	}
	s.server = &gortsplib.Server{
		Handler:     s,
		RTSPAddress: addr,
	}
	return s
}

// Start starts the RTSP server in the background.
func (s *RTSPServer) Start() error {
	err := s.server.Start()
	if err != nil {
		return err
	}
	log.Info().Str("addr", s.server.RTSPAddress).Msg("RTSP server started")
	return nil
}

// Close tears down all streams and stops the server.
func (s *RTSPServer) Close() {
	s.mu.Lock()
	for uid, entry := range s.streams {
		entry.stream.Close()
		delete(s.streams, uid)
	}
	s.mu.Unlock()
	s.server.Close()
}

// RegisterStream creates a new RTSP stream for the given baby with the provided SPS/PPS.
func (s *RTSPServer) RegisterStream(babyUID string, sps, pps []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Close existing stream if any
	if existing, ok := s.streams[babyUID]; ok {
		existing.stream.Close()
		delete(s.streams, babyUID)
	}

	forma := &format.H264{
		PayloadTyp:        96,
		PacketizationMode: 1,
		SPS:               sps,
		PPS:               pps,
	}

	enc, err := forma.CreateEncoder()
	if err != nil {
		return err
	}

	medi := &description.Media{
		Type:    description.MediaTypeVideo,
		Formats: []format.Format{forma},
	}

	stream := &gortsplib.ServerStream{
		Server: s.server,
		Desc: &description.Session{
			Medias: []*description.Media{medi},
		},
	}

	if err := stream.Initialize(); err != nil {
		return err
	}

	s.streams[babyUID] = &streamEntry{
		stream: stream,
		media:  medi,
		forma:  forma,
		enc:    enc,
	}

	log.Info().Str("baby_uid", babyUID).Msg("RTSP stream registered")
	return nil
}

// WriteH264 encodes H264 NALUs into RTP and writes them to the stream for the given baby.
func (s *RTSPServer) WriteH264(babyUID string, nalus [][]byte, pts time.Duration) {
	s.mu.RLock()
	entry, ok := s.streams[babyUID]
	s.mu.RUnlock()

	if !ok {
		return
	}

	packets, err := entry.enc.Encode(nalus)
	if err != nil {
		return
	}

	rtpTimestamp := uint32(pts.Seconds() * 90000)
	for _, pkt := range packets {
		pkt.Timestamp = rtpTimestamp
		entry.stream.WritePacketRTP(entry.media, pkt)
	}
}

// UnregisterStream tears down the RTSP stream for the given baby.
func (s *RTSPServer) UnregisterStream(babyUID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if entry, ok := s.streams[babyUID]; ok {
		entry.stream.Close()
		delete(s.streams, babyUID)
		log.Info().Str("baby_uid", babyUID).Msg("RTSP stream unregistered")
	}
}

// --- gortsplib ServerHandler interface ---

func (s *RTSPServer) OnConnOpen(_ *gortsplib.ServerHandlerOnConnOpenCtx) {
	log.Debug().Msg("RTSP client connected")
}

func (s *RTSPServer) OnConnClose(ctx *gortsplib.ServerHandlerOnConnCloseCtx) {
	log.Debug().Err(ctx.Error).Msg("RTSP client disconnected")
}

func (s *RTSPServer) OnSessionOpen(_ *gortsplib.ServerHandlerOnSessionOpenCtx) {
	log.Debug().Msg("RTSP session opened")
}

func (s *RTSPServer) OnSessionClose(_ *gortsplib.ServerHandlerOnSessionCloseCtx) {
	log.Debug().Msg("RTSP session closed")
}

func (s *RTSPServer) OnDescribe(ctx *gortsplib.ServerHandlerOnDescribeCtx) (*base.Response, *gortsplib.ServerStream, error) {
	babyUID := extractBabyUID(ctx.Path)
	if babyUID == "" {
		return &base.Response{StatusCode: base.StatusNotFound}, nil, nil
	}

	s.mu.RLock()
	entry, ok := s.streams[babyUID]
	s.mu.RUnlock()

	if !ok {
		log.Debug().Str("baby_uid", babyUID).Msg("RTSP DESCRIBE: stream not found")
		return &base.Response{StatusCode: base.StatusNotFound}, nil, nil
	}

	return &base.Response{StatusCode: base.StatusOK}, entry.stream, nil
}

func (s *RTSPServer) OnSetup(ctx *gortsplib.ServerHandlerOnSetupCtx) (*base.Response, *gortsplib.ServerStream, error) {
	babyUID := extractBabyUID(ctx.Path)
	if babyUID == "" {
		return &base.Response{StatusCode: base.StatusNotFound}, nil, nil
	}

	s.mu.RLock()
	entry, ok := s.streams[babyUID]
	s.mu.RUnlock()

	if !ok {
		return &base.Response{StatusCode: base.StatusNotFound}, nil, nil
	}

	return &base.Response{StatusCode: base.StatusOK}, entry.stream, nil
}

func (s *RTSPServer) OnPlay(_ *gortsplib.ServerHandlerOnPlayCtx) (*base.Response, error) {
	return &base.Response{StatusCode: base.StatusOK}, nil
}

var rtspPathRX = regexp.MustCompile(`^/local/([a-z0-9_-]+)`)

func extractBabyUID(path string) string {
	matches := rtspPathRX.FindStringSubmatch(path)
	if len(matches) != 2 {
		return ""
	}
	return matches[1]
}
