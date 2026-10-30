package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadConfig(t *testing.T) {
	for _, tc := range []struct {
		name  string
		env   map[string]string
		valid bool
	}{
		{"required", map[string]string{}, false},
		{"defaults", map[string]string{"DATABASE_URL": "postgres://localhost/catalog"}, true},
		{"custom", map[string]string{"DATABASE_URL": "postgres://localhost/catalog", "HTTP_ADDR": ":9000", "DB_MAX_CONNS": "20"}, true},
		{"zero", map[string]string{"DATABASE_URL": "x", "DB_MAX_CONNS": "0"}, false},
		{"large", map[string]string{"DATABASE_URL": "x", "DB_MAX_CONNS": "101"}, false},
		{"invalid", map[string]string{"DATABASE_URL": "x", "DB_MAX_CONNS": "abc"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := LoadConfig(func(key string) string { return tc.env[key] })
			if !tc.valid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tc.name == "defaults" {
				require.Equal(t, ":8080", c.HTTPAddr)
				require.Equal(t, int32(10), c.MaxConns)
			} else {
				require.Equal(t, ":9000", c.HTTPAddr)
				require.Equal(t, int32(20), c.MaxConns)
			}
		})
	}
}

func TestRunStartupFailures(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	err := Run(context.Background(), Config{DatabaseURL: "://invalid", MaxConns: 1}, log)
	require.EqualError(t, err, "invalid DATABASE_URL")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = Run(ctx, Config{DatabaseURL: "postgres://localhost:1/catalog?sslmode=disable", MaxConns: 1}, log)
	require.EqualError(t, err, "database unavailable at startup")
}

type fakeServer struct {
	started     chan struct{}
	stopped     chan struct{}
	listenErr   error
	shutdownErr error
	closed      bool
	deadline    bool
}

func (s *fakeServer) ListenAndServe() error {
	close(s.started)
	if s.listenErr != nil {
		return s.listenErr
	}
	<-s.stopped
	return http.ErrServerClosed
}
func (s *fakeServer) Shutdown(ctx context.Context) error {
	_, s.deadline = ctx.Deadline()
	if s.shutdownErr != nil {
		return s.shutdownErr
	}
	close(s.stopped)
	return nil
}
func (s *fakeServer) Close() error { s.closed = true; close(s.stopped); return nil }

func TestServe(t *testing.T) {
	for _, listenErr := range []error{http.ErrServerClosed, errors.New("bind failed")} {
		s := &fakeServer{started: make(chan struct{}), listenErr: listenErr}
		err := Serve(context.Background(), s)
		if errors.Is(listenErr, http.ErrServerClosed) {
			require.NoError(t, err)
		} else {
			require.ErrorIs(t, err, listenErr)
		}
	}
	for _, shutdownErr := range []error{nil, errors.New("drain timeout")} {
		s := &fakeServer{started: make(chan struct{}), stopped: make(chan struct{}), shutdownErr: shutdownErr}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		result := make(chan error, 1)
		go func() { result <- Serve(ctx, s) }()
		<-s.started
		cancel()
		err := <-result
		require.True(t, s.deadline)
		if shutdownErr == nil {
			require.NoError(t, err)
			require.False(t, s.closed)
		} else {
			require.ErrorIs(t, err, shutdownErr)
			require.True(t, s.closed)
		}
	}
}
