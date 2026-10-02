package api

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWaitAfterServerClose(t *testing.T) {
	a, err := NewAPI(nil, nil, "127.0.0.1:0", slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	a.Run(t.Context())
	require.NoError(t, a.srv.Close())
	done := make(chan error, 1)
	go func() { done <- a.Wait() }()
	select {
	case waitErr := <-done:
		require.NoError(t, waitErr)
	case <-time.After(5 * time.Second):
		t.Fatal("API did not stop after closing the server")
	}
	a.Shutdown()
}
