package app

import (
	"time"

	"github.com/rs/zerolog/log"

	"github.com/gregory-m/nanit/pkg/client"
	"github.com/gregory-m/nanit/pkg/utils"
)

// Reconcile sound state on connection and every 30 seconds; the camera does not
// always push playback changes made by another client. Await each GET so request
// handlers are cleaned up on success, rejection, or timeout.
func pollPlayback(babyUID string, conn *client.WebsocketConnection, ctx utils.GracefulContext) {
	poll := func() {
		await := conn.SendRequest(client.RequestType_GET_PLAYBACK, &client.Request{})
		if _, err := await(10 * time.Second); err != nil {
			log.Debug().Err(err).Str("baby_uid", babyUID).Msg("Playback state query failed")
		}
	}
	poll()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			poll()
		}
	}
}
