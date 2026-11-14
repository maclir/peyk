package agent

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Listen opens the socket hooks ping. A socket file left by an agent that
// died is replaced; one with a live agent behind it is an error, because two
// agents would publish over each other.
func Listen(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if conn, err := net.DialTimeout("unix", path, 200*time.Millisecond); err == nil {
		conn.Close()
		return nil, fmt.Errorf("another peyk agent is running (%s)", path)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return net.Listen("unix", path)
}

// Serve turns each connection into one ping. Pings arriving while one is
// pending are dropped: the pending refresh covers them.
func Serve(ctx context.Context, ln net.Listener, pings chan<- struct{}) {
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		conn.Close()
		select {
		case pings <- struct{}{}:
		default:
		}
	}
}

// Ping tells a running agent that something changed. With no agent running it
// does nothing: hooks fire whether or not peyk is on.
func Ping(path string) {
	conn, err := net.DialTimeout("unix", path, 200*time.Millisecond)
	if err != nil {
		return
	}
	conn.Close()
}
