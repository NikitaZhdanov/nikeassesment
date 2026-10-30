package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nikitazhdanov/nikeassesment/internal/httpapi"
	"github.com/nikitazhdanov/nikeassesment/internal/postgres"
)

type Config struct {
	DatabaseURL string
	HTTPAddr    string
	MaxConns    int32
}

func LoadConfig(getenv func(string) string) (Config, error) {
	c := Config{DatabaseURL: getenv("DATABASE_URL"), HTTPAddr: getenv("HTTP_ADDR"), MaxConns: 10}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required")
	}
	if c.HTTPAddr == "" {
		c.HTTPAddr = ":8080"
	}
	if value := getenv("DB_MAX_CONNS"); value != "" {
		n, err := strconv.ParseInt(value, 10, 32)
		if err != nil || n < 1 || n > 100 {
			return c, errors.New("DB_MAX_CONNS must be between 1 and 100")
		}
		c.MaxConns = int32(n)
	}
	return c, nil
}

func Run(ctx context.Context, c Config, log *slog.Logger) error {
	poolConfig, err := pgxpool.ParseConfig(c.DatabaseURL)
	if err != nil {
		return errors.New("invalid DATABASE_URL")
	}
	poolConfig.MaxConns = c.MaxConns
	poolConfig.ConnConfig.ConnectTimeout = 5 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return errors.New("cannot initialize database pool")
	}
	defer pool.Close()
	startup, cancel := context.WithTimeout(ctx, 10*time.Second)
	err = pool.Ping(startup)
	cancel()
	if err != nil {
		return errors.New("database unavailable at startup")
	}
	server := &http.Server{
		Addr: c.HTTPAddr, Handler: httpapi.New(postgres.New(pool), pool.Ping, log),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10,
	}
	log.Info("starting catalog", "address", c.HTTPAddr)
	return Serve(ctx, server)
}

type Server interface {
	ListenAndServe() error
	Shutdown(context.Context) error
	Close() error
}

// Serve owns server lifetime; cancellation drains in-flight requests before close.
func Serve(ctx context.Context, server Server) error {
	result := make(chan error, 1)
	go func() { result <- server.ListenAndServe() }()
	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
			<-result
			return fmt.Errorf("shutdown: %w", err)
		}
		<-result
		return nil
	}
}
