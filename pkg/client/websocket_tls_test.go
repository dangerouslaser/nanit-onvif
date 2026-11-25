package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/sacOO7/gowebsocket"
)

// The websocket carries the account's auth token, so a server whose certificate does not verify (here:
// the test server's self-signed one, standing in for anyone impersonating api.nanit.com) must be refused.
func TestSocketRejectsUntrustedCertificate(t *testing.T) {
	upgrader := websocket.Upgrader{}
	sawAuthorization := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuthorization = r.Header.Get("Authorization") != ""
		if conn, err := upgrader.Upgrade(w, r, nil); err == nil {
			conn.Close()
		}
	}))
	defer server.Close()

	connected := false
	var connectErr error

	socket := newSocket("wss"+strings.TrimPrefix(server.URL, "https"), "Bearer secret")
	socket.OnConnected = func(gowebsocket.Socket) { connected = true }
	socket.OnConnectError = func(err error, _ gowebsocket.Socket) { connectErr = err }
	socket.Connect() // dials synchronously

	if connected {
		socket.Close()
		t.Fatal("connected to a server with an untrusted certificate")
	}
	if connectErr == nil {
		t.Fatal("expected a certificate error")
	}
	if sawAuthorization {
		t.Fatal("the auth token was sent to a server with an untrusted certificate")
	}
}
