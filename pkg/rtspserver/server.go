package rtspserver

import (
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph264"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtpmpeg4audio"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"
	"github.com/rs/zerolog/log"
)

var annexBStartCode = []byte{0x00, 0x00, 0x00, 0x01}

type streamEntry struct {
	stream       *gortsplib.ServerStream
	videoMedia   *description.Media
	videoForma   *format.H264
	videoEnc     *rtph264.Encoder
	audioMedia   *description.Media
	audioForma   *format.MPEG4Audio
	audioEnc     *rtpmpeg4audio.Encoder
	lastKeyframe atomic.Value // []byte: annex-B SPS+PPS+IDR access unit
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

// RegisterStream creates a new RTSP stream for the given baby.
// If aacConfig is non-nil, an audio track is included alongside video.
func (s *RTSPServer) RegisterStream(babyUID string, sps, pps, aacConfig []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Close existing stream if any
	if existing, ok := s.streams[babyUID]; ok {
		existing.stream.Close()
		delete(s.streams, babyUID)
	}

	videoForma := &format.H264{
		PayloadTyp:        96,
		PacketizationMode: 1,
		SPS:               sps,
		PPS:               pps,
	}

	videoEnc, err := videoForma.CreateEncoder()
	if err != nil {
		return err
	}

	videoMedia := &description.Media{
		Type:    description.MediaTypeVideo,
		Formats: []format.Format{videoForma},
	}

	entry := &streamEntry{
		videoMedia: videoMedia,
		videoForma: videoForma,
		videoEnc:   videoEnc,
	}

	medias := []*description.Media{videoMedia}

	// Add audio track if AAC config is provided
	if len(aacConfig) > 0 {
		var asc mpeg4audio.AudioSpecificConfig
		if err := asc.Unmarshal(aacConfig); err != nil {
			log.Warn().Err(err).Msg("Failed to parse AAC config for RTSP, registering video only")
		} else {
			audioForma := &format.MPEG4Audio{
				PayloadTyp:       97,
				Config:           &asc,
				SizeLength:       13,
				IndexLength:      3,
				IndexDeltaLength: 3,
			}
			audioEnc, err := audioForma.CreateEncoder()
			if err != nil {
				log.Warn().Err(err).Msg("Failed to create AAC encoder for RTSP, registering video only")
			} else {
				audioMedia := &description.Media{
					Type:    description.MediaTypeAudio,
					Formats: []format.Format{audioForma},
				}
				medias = append(medias, audioMedia)
				entry.audioMedia = audioMedia
				entry.audioForma = audioForma
				entry.audioEnc = audioEnc
			}
		}
	}

	stream := &gortsplib.ServerStream{
		Server: s.server,
		Desc: &description.Session{
			Medias: medias,
		},
	}

	if err := stream.Initialize(); err != nil {
		return err
	}

	entry.stream = stream
	s.streams[babyUID] = entry

	log.Info().Str("baby_uid", babyUID).Bool("audio", entry.audioEnc != nil).Msg("RTSP stream registered")
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

	if containsIDR(nalus) {
		entry.lastKeyframe.Store(buildAnnexBKeyframe(entry.videoForma.SPS, entry.videoForma.PPS, nalus))
	}

	packets, err := entry.videoEnc.Encode(nalus)
	if err != nil {
		return
	}

	rtpTimestamp := uint32(pts.Seconds() * 90000)
	for _, pkt := range packets {
		pkt.Timestamp = rtpTimestamp
		entry.stream.WritePacketRTP(entry.videoMedia, pkt)
	}
}

// GetKeyframe returns the most recent H.264 IDR access unit (annex-B encoded
// SPS+PPS+IDR) for the given baby, if any has been observed.
func (s *RTSPServer) GetKeyframe(babyUID string) ([]byte, bool) {
	s.mu.RLock()
	entry, ok := s.streams[babyUID]
	s.mu.RUnlock()

	if !ok {
		return nil, false
	}

	v := entry.lastKeyframe.Load()
	if v == nil {
		return nil, false
	}
	kf, _ := v.([]byte)
	if len(kf) == 0 {
		return nil, false
	}
	return kf, true
}

func containsIDR(nalus [][]byte) bool {
	for _, n := range nalus {
		if len(n) > 0 && n[0]&0x1F == 5 {
			return true
		}
	}
	return false
}

func buildAnnexBKeyframe(sps, pps []byte, nalus [][]byte) []byte {
	size := 0
	if len(sps) > 0 {
		size += len(annexBStartCode) + len(sps)
	}
	if len(pps) > 0 {
		size += len(annexBStartCode) + len(pps)
	}
	for _, n := range nalus {
		size += len(annexBStartCode) + len(n)
	}
	buf := make([]byte, 0, size)
	if len(sps) > 0 {
		buf = append(buf, annexBStartCode...)
		buf = append(buf, sps...)
	}
	if len(pps) > 0 {
		buf = append(buf, annexBStartCode...)
		buf = append(buf, pps...)
	}
	for _, n := range nalus {
		buf = append(buf, annexBStartCode...)
		buf = append(buf, n...)
	}
	return buf
}

// WriteAAC encodes an AAC access unit into RTP and writes it to the stream for the given baby.
func (s *RTSPServer) WriteAAC(babyUID string, au []byte, pts time.Duration) {
	s.mu.RLock()
	entry, ok := s.streams[babyUID]
	s.mu.RUnlock()

	if !ok || entry.audioEnc == nil {
		return
	}

	packets, err := entry.audioEnc.Encode([][]byte{au})
	if err != nil {
		return
	}

	clockRate := entry.audioForma.ClockRate()
	rtpTimestamp := uint32(pts.Seconds() * float64(clockRate))
	for _, pkt := range packets {
		pkt.Timestamp = rtpTimestamp
		entry.stream.WritePacketRTP(entry.audioMedia, pkt)
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
