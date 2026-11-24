package client

import (
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

// WhiteNoisePlayback builds the camera's built-in white-noise command without
// changing its volume. Duration 0 keeps playing until stopped.
//
// The older generated schema omits Playback.duration (2), Playback.track (3),
// and Response.playback (11). Encode those documented fields as protobuf unknown
// fields so the existing generated types remain compatible and unmodified.
// Schema: https://github.com/wealthystudent/ha-nanit/blob/main/packages/aionanit/proto/nanit.proto
func WhiteNoisePlayback(on bool) *Playback {
	playback := &Playback{Status: Playback_STOPPED.Enum()}
	if !on {
		return playback
	}
	playback.Status = Playback_STARTED.Enum()

	// Soundtrack.type = 0 (built-in file), Soundtrack.filename = White Noise.wav.
	track := protowire.AppendTag(nil, 1, protowire.VarintType)
	track = protowire.AppendVarint(track, 0)
	track = protowire.AppendTag(track, 2, protowire.BytesType)
	track = protowire.AppendString(track, "White Noise.wav")

	fields := protowire.AppendTag(nil, 2, protowire.VarintType)
	fields = protowire.AppendVarint(fields, 0)
	fields = protowire.AppendTag(fields, 3, protowire.BytesType)
	fields = protowire.AppendBytes(fields, track)
	playback.ProtoReflect().SetUnknown(fields)
	return playback
}

// ResponsePlayback decodes playback reports retained by the older schema as
// unknown fields. A response with no playback report returns nil, never "off".
func ResponsePlayback(response *Response) (*Playback, error) {
	if response == nil {
		return nil, nil
	}
	fields := response.ProtoReflect().GetUnknown()
	var playback *Playback
	for len(fields) > 0 {
		number, kind, n := protowire.ConsumeTag(fields)
		if n < 0 {
			return nil, protowire.ParseError(n)
		}
		fields = fields[n:]
		if number == 11 && kind == protowire.BytesType {
			data, size := protowire.ConsumeBytes(fields)
			if size < 0 {
				return nil, protowire.ParseError(size)
			}
			playback = &Playback{}
			if err := proto.Unmarshal(data, playback); err != nil {
				return nil, err
			}
			fields = fields[size:]
			continue
		}
		size := protowire.ConsumeFieldValue(number, kind, fields)
		if size < 0 {
			return nil, protowire.ParseError(size)
		}
		fields = fields[size:]
	}
	return playback, nil
}
