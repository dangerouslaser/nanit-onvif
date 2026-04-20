package app

import (
	"time"

	"github.com/rs/zerolog/log"

	"github.com/gregory-m/nanit/pkg/baby"
	"github.com/gregory-m/nanit/pkg/client"
	"github.com/gregory-m/nanit/pkg/utils"
)

func processSensorData(babyUID string, sensorData []*client.SensorData, stateManager *baby.StateManager) {
	// Parse sensor update
	stateUpdate := baby.State{}
	for _, s := range sensorData {
		if s.SensorType == nil {
			continue
		}
		switch *s.SensorType {
		case client.SensorType_TEMPERATURE:
			if s.ValueMilli != nil {
				stateUpdate.SetTemperatureMilli(*s.ValueMilli)
			}
		case client.SensorType_HUMIDITY:
			if s.ValueMilli != nil {
				stateUpdate.SetHumidityMilli(*s.ValueMilli)
			}
		case client.SensorType_NIGHT:
			if s.Value != nil {
				stateUpdate.SetIsNight(*s.Value == 1)
			}
		case client.SensorType_LIGHT:
			if s.Value != nil {
				stateUpdate.SetLightLevel(*s.Value)
			}
		case client.SensorType_MOTION:
			if s.IsAlert != nil {
				stateUpdate.SetMotionDetected(*s.IsAlert)
			}
			if s.Timestamp != nil && *s.IsAlert {
				stateUpdate.SetMotionTimestamp(*s.Timestamp)
			}
		case client.SensorType_SOUND:
			if s.IsAlert != nil {
				stateUpdate.SetSoundDetected(*s.IsAlert)
			}
			if s.Timestamp != nil && *s.IsAlert {
				stateUpdate.SetSoundTimestamp(*s.Timestamp)
			}
		}
	}

	stateManager.Update(babyUID, stateUpdate)
}

func processControl(babyUID string, control *client.Control, stateManager *baby.StateManager) {
	if control == nil {
		return
	}
	stateUpdate := baby.State{}
	if control.NightLight != nil {
		stateUpdate.SetNightLightOn(*control.NightLight == client.Control_LIGHT_ON)
	}
	if control.NightLightTimeout != nil {
		stateUpdate.SetNightLightTimeoutSec(*control.NightLightTimeout)
	}
	stateManager.Update(babyUID, stateUpdate)
}

func processSettings(babyUID string, settings *client.Settings, stateManager *baby.StateManager) {
	if settings == nil {
		return
	}
	stateUpdate := baby.State{}
	if settings.NightVision != nil {
		stateUpdate.SetNightVision(*settings.NightVision)
	}
	if settings.SleepMode != nil {
		stateUpdate.SetSleepMode(*settings.SleepMode)
	}
	if settings.StatusLightOn != nil {
		stateUpdate.SetStatusLightOn(*settings.StatusLightOn)
	}
	if settings.MicMuteOn != nil {
		stateUpdate.SetMicMuteOn(*settings.MicMuteOn)
	}
	if settings.Volume != nil {
		stateUpdate.SetVolume(*settings.Volume)
	}
	if settings.MountingMode != nil {
		stateUpdate.SetMountingMode(*settings.MountingMode)
	}
	stateManager.Update(babyUID, stateUpdate)
}

func processStatus(babyUID string, status *client.Status, stateManager *baby.StateManager) {
	if status == nil {
		return
	}
	stateUpdate := baby.State{}
	if status.CurrentVersion != nil {
		stateUpdate.SetFirmwareVersion(*status.CurrentVersion)
	}
	if status.HardwareVersion != nil {
		stateUpdate.SetHardwareVersion(*status.HardwareVersion)
	}
	if status.ConnectionToServer != nil {
		stateUpdate.SetIsConnectedToServer(*status.ConnectionToServer == client.Status_CONNECTED)
	}
	if status.Mode != nil {
		stateUpdate.SetMountingMode(int32(*status.Mode))
	}
	stateManager.Update(babyUID, stateUpdate)
}

func requestLocalStreaming(babyUID string, targetURL string, streamingStatus client.Streaming_Status, conn *client.WebsocketConnection, stateManager *baby.StateManager) {
	for {
		switch streamingStatus {
		case client.Streaming_STARTED:
			log.Info().Str("target", targetURL).Msg("Requesting local streaming")
		case client.Streaming_PAUSED:
			log.Info().Str("target", targetURL).Msg("Pausing local streaming")
		case client.Streaming_STOPPED:
			log.Info().Str("target", targetURL).Msg("Stopping local streaming")
		}

		awaitResponse := conn.SendRequest(client.RequestType_PUT_STREAMING, &client.Request{
			Streaming: &client.Streaming{
				Id:       client.StreamIdentifier(client.StreamIdentifier_MOBILE).Enum(),
				RtmpUrl:  utils.ConstRefStr(targetURL),
				Status:   client.Streaming_Status(streamingStatus).Enum(),
				Attempts: utils.ConstRefInt32(1),
			},
		})

		_, err := awaitResponse(30 * time.Second)

		if err != nil {
			if err.Error() == "Forbidden: Number of Mobile App connections above limit, declining connection" {
				log.Warn().Err(err).Msg("Too many app connections, waiting for local connection to become available...")
				stateManager.Update(babyUID, *baby.NewState().SetStreamRequestState(baby.StreamRequestState_RequestFailed))
                                time.Sleep(300 * time.Second)
				continue
			} else if err.Error() != "Request timeout" {
				if stateManager.GetBabyState(babyUID).GetStreamState() == baby.StreamState_Alive {
					log.Info().Err(err).Msg("Failed to request local streaming, but stream seems to be alive from previous run")
				} else if stateManager.GetBabyState(babyUID).GetStreamState() == baby.StreamState_Unhealthy {
					log.Error().Err(err).Msg("Failed to request local streaming and stream seems to be dead")
					stateManager.Update(babyUID, *baby.NewState().SetStreamRequestState(baby.StreamRequestState_RequestFailed))
				} else {
					log.Warn().Err(err).Msg("Failed to request local streaming, awaiting stream health check")
					stateManager.Update(babyUID, *baby.NewState().SetStreamRequestState(baby.StreamRequestState_RequestFailed))
				}

				return
			}

			if !stateManager.GetBabyState(babyUID).GetIsWebsocketAlive() {
				return
			}

			log.Warn().Msg("Streaming request timeout, trying again")

		} else {
			log.Info().Msg("Local streaming successfully requested")
			stateManager.Update(babyUID, *baby.NewState().SetStreamRequestState(baby.StreamRequestState_Requested))
			return
		}
	}
}
