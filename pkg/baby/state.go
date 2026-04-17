package baby

import (
	"github.com/rs/zerolog"
)

type StreamRequestState int32

const (
	StreamRequestState_NotRequested StreamRequestState = iota
	StreamRequestState_Requested
	StreamRequestState_RequestFailed
)

type StreamState int32

const (
	StreamState_Unknown StreamState = iota
	StreamState_Unhealthy
	StreamState_Alive
)

// State - struct holding information about state of a single baby
type State struct {
	StreamState        *StreamState        `internal:"true"`
	StreamRequestState *StreamRequestState `internal:"true"`
	IsWebsocketAlive   *bool               `internal:"true"`

	MotionTimestamp  *int32 // int32 is used to represent UTC timestamp
	SoundTimestamp   *int32 // int32 is used to represent UTC timestamp
	Temperature      *bool
	IsNight          *bool
	TemperatureMilli *int32
	HumidityMilli    *int32
}

// NewState - constructor
func NewState() *State {
	return &State{}
}

// Merge - Merges non-nil values of an argument to the state.
// Returns ptr to new state if changes, ptr to old state if not changed.
func (state *State) Merge(u *State) *State {
	changed := false
	out := *state // shallow copy: pointer fields are aliased until we rewrite them

	mergeStreamState(&out.StreamState, u.StreamState, &changed)
	mergeStreamRequestState(&out.StreamRequestState, u.StreamRequestState, &changed)
	mergeBool(&out.IsWebsocketAlive, u.IsWebsocketAlive, &changed)
	mergeInt32(&out.MotionTimestamp, u.MotionTimestamp, &changed)
	mergeInt32(&out.SoundTimestamp, u.SoundTimestamp, &changed)
	mergeBool(&out.Temperature, u.Temperature, &changed)
	mergeBool(&out.IsNight, u.IsNight, &changed)
	mergeInt32(&out.TemperatureMilli, u.TemperatureMilli, &changed)
	mergeInt32(&out.HumidityMilli, u.HumidityMilli, &changed)

	if !changed {
		return state
	}
	return &out
}

func mergeBool(dst **bool, src *bool, changed *bool) {
	if src == nil {
		return
	}
	if *dst == nil || **dst != *src {
		v := *src
		*dst = &v
		*changed = true
	}
}

func mergeInt32(dst **int32, src *int32, changed *bool) {
	if src == nil {
		return
	}
	if *dst == nil || **dst != *src {
		v := *src
		*dst = &v
		*changed = true
	}
}

func mergeStreamState(dst **StreamState, src *StreamState, changed *bool) {
	if src == nil {
		return
	}
	if *dst == nil || **dst != *src {
		v := *src
		*dst = &v
		*changed = true
	}
}

func mergeStreamRequestState(dst **StreamRequestState, src *StreamRequestState, changed *bool) {
	if src == nil {
		return
	}
	if *dst == nil || **dst != *src {
		v := *src
		*dst = &v
		*changed = true
	}
}

// AsMap - returns K/V map of non-nil properties. Internal fields are
// excluded unless includeInternal is true.
func (state *State) AsMap(includeInternal bool) map[string]interface{} {
	m := make(map[string]interface{})

	if includeInternal {
		if state.StreamState != nil {
			m["stream_state"] = int64(*state.StreamState)
		}
		if state.StreamRequestState != nil {
			m["stream_request_state"] = int64(*state.StreamRequestState)
		}
		if state.IsWebsocketAlive != nil {
			m["is_websocket_alive"] = *state.IsWebsocketAlive
		}
	}

	if state.MotionTimestamp != nil {
		m["motion_timestamp"] = int64(*state.MotionTimestamp)
	}
	if state.SoundTimestamp != nil {
		m["sound_timestamp"] = int64(*state.SoundTimestamp)
	}
	if state.Temperature != nil {
		m["temperature"] = *state.Temperature
	}
	if state.IsNight != nil {
		m["is_night"] = *state.IsNight
	}
	if state.TemperatureMilli != nil {
		m["temperature"] = float64(*state.TemperatureMilli) / 1000
	}
	if state.HumidityMilli != nil {
		m["humidity"] = float64(*state.HumidityMilli) / 1000
	}

	return m
}

// EnhanceLogEvent - appends non-nil properties to a log event
func (state *State) EnhanceLogEvent(e *zerolog.Event) *zerolog.Event {
	for key, value := range state.AsMap(true) {
		e.Interface(key, value)
	}

	return e
}

// SetTemperatureMilli - mutates field, returns itself
func (state *State) SetTemperatureMilli(value int32) *State {
	state.TemperatureMilli = &value
	return state
}

// GetTemperature - returns temperature as floating point
func (state *State) GetTemperature() float64 {
	if state.TemperatureMilli != nil {
		return float64(*state.TemperatureMilli) / 1000
	}

	return 0
}

// SetHumidityMilli - mutates field, returns itself
func (state *State) SetHumidityMilli(value int32) *State {
	state.HumidityMilli = &value
	return state
}

// GetHumidity - returns humidity as floating point
func (state *State) GetHumidity() float64 {
	if state.HumidityMilli != nil {
		return float64(*state.HumidityMilli) / 1000
	}

	return 0
}

// SetStreamRequestState - mutates field, returns itself
func (state *State) SetStreamRequestState(value StreamRequestState) *State {
	state.StreamRequestState = &value
	return state
}

// GetStreamRequestState - safely returns value
func (state *State) GetStreamRequestState() StreamRequestState {
	if state.StreamRequestState != nil {
		return *state.StreamRequestState
	}

	return StreamRequestState_NotRequested
}

// SetStreamState - mutates field, returns itself
func (state *State) SetStreamState(value StreamState) *State {
	state.StreamState = &value
	return state
}

// GetStreamState - safely returns value
func (state *State) GetStreamState() StreamState {
	if state.StreamState != nil {
		return *state.StreamState
	}

	return StreamState_Unknown
}

// SetIsNight - mutates field, returns itself
func (state *State) SetIsNight(value bool) *State {
	state.IsNight = &value
	return state
}

func (state *State) SetMotionTimestamp(value int32) *State {
	state.MotionTimestamp = &value
	return state
}

func (state *State) SetSoundTimestamp(value int32) *State {
	state.SoundTimestamp = &value
	return state
}

func (state *State) SetTemperature(value bool) *State {
	state.Temperature = &value
	return state
}

// GetIsWebsocketAlive - safely returns value
func (state *State) GetIsWebsocketAlive() bool {
	if state.IsWebsocketAlive != nil {
		return *state.IsWebsocketAlive
	}

	return false
}

// SetWebsocketAlive - mutates field, returns itself
func (state *State) SetWebsocketAlive(value bool) *State {
	state.IsWebsocketAlive = &value
	return state
}
