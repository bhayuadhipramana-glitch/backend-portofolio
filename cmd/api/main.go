package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"golang.org/x/sync/errgroup"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/platform/postgres"
)

const (
	defaultPort = "8080"

	readHeaderTimeout = 5 * time.Second
	readTimeout       = 10 * time.Second
	writeTimeout      = 15 * time.Second
	idleTimeout       = 60 * time.Second

	shutdownTimeout   = 15 * time.Second
	readyCheckTimeout = 2 * time.Second
)

// pinger is the only database capability the readiness probe needs.
type pinger interface {
	Ping(ctx context.Context) error
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if err := run(logger); err != nil {
		logger.Error("api stopped with error", "error", err)
		os.Exit(1)
	}
	logger.Info("api stopped cleanly")
}

// run wires the dependencies and blocks until the server has shut down.
func run(logger *slog.Logger) error {
	// A missing .env file is not an error: deployed environments
	// provide real environment variables instead.
	_ = godotenv.Load()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := connectDatabase(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	logger.Info("database connected")

	srv := newServer(newRouter(pool))

	return serve(ctx, logger, srv)
}

func connectDatabase(ctx context.Context) (*pgxpool.Pool, error) {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		return nil, errors.New("environment variable DB_DSN is not set")
	}

	pool, err := postgres.Setup(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect database: %w", err)
	}
	return pool, nil
}

func newRouter(db pinger) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/healthz", handleHealthz)
	r.Get("/readyz", handleReadyz(db))

	return r
}

// handleHealthz reports that the process is alive. It does not check
// dependencies, because restarting the API cannot fix a failed database.
func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// handleReadyz reports whether the API can serve traffic right now.
func handleReadyz(db pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readyCheckTimeout)
		defer cancel()

		if err := db.Ping(ctx); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	}
}

func newServer(handler http.Handler) *http.Server {
	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}

	return &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
}

// serve runs the server until ctx is cancelled, then shuts it down gracefully.
func serve(ctx context.Context, logger *slog.Logger, srv *http.Server) error {
	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		logger.Info("http server started", "addr", srv.Addr)

		// ErrServerClosed is the expected result of a call to Shutdown.
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http serve: %w", err)
		}
		return nil
	})

	g.Go(func() error {
		<-gctx.Done()
		logger.Info("shutdown started", "timeout", shutdownTimeout.String())

		// The parent context is already cancelled, so shutdown needs its own.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			_ = srv.Close()
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		return nil
	})

	return g.Wait()
}