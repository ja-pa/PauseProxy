package mitm

import (
	"net/http"
	"time"

	"golang.org/x/net/http2"
)

// NewUpstreamTransport bounds stalled upstream operations and retires dead
// HTTP/2 connections, including sockets left behind after suspend/resume.
func NewUpstreamTransport() (*http.Transport, error) {
	return newUpstreamTransport(15*time.Second, 5*time.Second, 10*time.Second, 15*time.Second)
}

func newUpstreamTransport(readIdle, ping, write, headers time.Duration) (*http.Transport, error) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.DisableCompression = true
	tr.ResponseHeaderTimeout = headers
	h2, err := http2.ConfigureTransports(tr)
	if err != nil {
		return nil, err
	}
	h2.ReadIdleTimeout = readIdle
	h2.PingTimeout = ping
	h2.WriteByteTimeout = write
	return tr, nil
}
