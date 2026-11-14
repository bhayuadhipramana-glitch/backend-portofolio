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
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"golang.org/x/sync/errgroup"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/handler"
	authmw "github.com/bhayuadhipramana-glicth/backend-portofolio/internal/middleware"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/platform/github"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/platform/postgres"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/platform/valkey"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/usecase"
)

const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 10 * time.Second
	// writeTimeout must stay above requestTimeout so a handler that hits its
	// deadline can still write the 503 response before the connection closes.
	writeTimeout      = 30 * time.Second
	requestTimeout    = 25 * time.Second
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
	_ = godotenv.Load()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	// 1. Database connection.
	pool, err := connectDatabase(ctx, cfg.databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	logger.Info("database connected", "app_env", cfg.appEnv)

	// 2. Cache: in-process L1 in front of Valkey. Valkey is required at boot
	// (fail fast, like the database); once running, an outage degrades reads
	// to the database instead of failing them.
	valkeyClient, err := valkey.Connect(cfg.valkeyAddr)
	if err != nil {
		return err
	}
	defer valkeyClient.Close()
	contentCache := valkey.NewCache(valkeyClient, valkey.Config{})
	logger.Info("valkey connected", "addr", cfg.valkeyAddr)

	// 3. Dependency injection: repository -> usecase -> handler.
	projectUsecase, err := usecase.NewProjectUsecase(postgres.NewProjectRepository(pool), contentCache)
	if err != nil {
		return fmt.Errorf("init project usecase: %w", err)
	}

	articleUsecase, err := usecase.NewArticleUsecase(postgres.NewArticleRepository(pool), contentCache)
	if err != nil {
		return fmt.Errorf("init article usecase: %w", err)
	}

	profileUsecase, err := usecase.NewProfileUsecase(postgres.NewProfileRepository(pool), contentCache)
	if err != nil {
		return fmt.Errorf("init profile usecase: %w", err)
	}

	resumeUsecase, err := usecase.NewResumeUsecase(postgres.NewResumeRepository(pool), contentCache)
	if err != nil {
		return fmt.Errorf("init resume usecase: %w", err)
	}

	githubClient := github.New(cfg.clientID, cfg.clientSecret, cfg.redirectURL)
	authUsecase, err := usecase.NewAuthUsecase(githubClient, postgres.NewSessionRepository(pool), cfg.adminGithubID, sessionTTL)
	if err != nil {
		return fmt.Errorf("init auth usecase: %w", err)
	}

	// 4. Router and handlers.
	router := newRouter(routerDeps{
		db:              pool,
		cache:           contentCache,
		appEnv:          cfg.appEnv,
		trustCloudflare: cfg.trustCloudflare,
		projectHandler:  handler.NewProjectHandler(projectUsecase),
		articleHandler:  handler.NewArticleHandler(articleUsecase),
		profileHandler:  handler.NewProfileHandler(profileUsecase),
		resumeHandler:   handler.NewResumeHandler(resumeUsecase),
		authHandler:     handler.NewAuthHandler(authUsecase, cfg.appEnv, cfg.successURL, sessionTTL),
		authenticator:   authUsecase,
		allowedOrigins:  cfg.allowedOrigin,
	})

	return serve(ctx, logger, newServer(router, cfg.port))
}

func connectDatabase(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	pool, err := postgres.Setup(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect database: %w", err)
	}
	return pool, nil
}

// routerDeps carries everything newRouter needs to build the HTTP routes.
type routerDeps struct {
	db              pinger
	cache           pinger // Valkey health; an outage degrades readiness, it does not fail it
	appEnv          string
	trustCloudflare bool
	projectHandler  *handler.ProjectHandler
	articleHandler  *handler.ArticleHandler
	profileHandler  *handler.ProfileHandler
	resumeHandler   *handler.ResumeHandler
	authHandler     *handler.AuthHandler
	authenticator   authmw.Authenticator
	allowedOrigins  []string
}

func newRouter(d routerDeps) http.Handler {
	r := chi.NewRouter()

	// CORS goes first so it intercepts preflight requests. Origins must be
	// explicit: with AllowCredentials a wildcard would let any site use the
	// admin session cookie.
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   d.allowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link", authmw.CacheHeaderName},
		AllowCredentials: true,
		MaxAge:           300, // seconds a browser may cache the preflight result
	}))

	// Logging and stability middleware. ClientIP replaces chi's RealIP: it only
	// trusts CF-Connecting-IP, and only when running behind Cloudflare Tunnel.
	r.Use(middleware.RequestID)
	r.Use(authmw.ClientIP(d.trustCloudflare))
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(requestTimeout))

	// Infrastructure routes.
	r.Get("/healthz", handleHealthz)
	r.Get("/readyz", handleReadyz(d.db, d.cache))

	requireAdmin := authmw.RequireAdmin(d.authenticator, d.appEnv)
	trustedOrigin := authmw.TrustedOrigin(d.allowedOrigins)

	r.Route("/api/v1", func(r chi.Router) {
		// Auth: the login flow is public; session inspection and logout need a session.
		r.Route("/auth", func(r chi.Router) {
			r.Get("/github/login", d.authHandler.Login)
			r.Get("/github/callback", d.authHandler.Callback)

			r.Group(func(r chi.Router) {
				r.Use(trustedOrigin, requireAdmin)
				r.Get("/me", d.authHandler.Me)
				r.Post("/logout", d.authHandler.Logout)
			})
		})

		// Public reads: published articles and the active profile/resume only.
		// Lists accept ?limit= (default 20, max 100) and ?offset=.
		// They are cache-aside reads and carry an X-Cache header.
		r.Group(func(r chi.Router) {
			r.Use(authmw.CacheStatus)

			r.Get("/projects", d.projectHandler.List)
			r.Get("/projects/{id}", d.projectHandler.GetByID)
			r.Get("/articles", d.articleHandler.List)
			r.Get("/articles/{id}", d.articleHandler.GetByID) // {id} is a UUID or a slug
			r.Get("/profiles", d.profileHandler.List)
			r.Get("/profiles/{id}", d.profileHandler.GetByID)
			r.Get("/resumes", d.resumeHandler.List)
			r.Get("/resumes/{id}", d.resumeHandler.GetByID)
		})

		// Admin-only: unfiltered reads and all mutations.
		r.Group(func(r chi.Router) {
			r.Use(trustedOrigin, requireAdmin)

			r.Get("/admin/articles", d.articleHandler.AdminList)
			r.Get("/admin/articles/{id}", d.articleHandler.AdminGetByID)
			r.Get("/admin/profiles", d.profileHandler.AdminList)
			r.Get("/admin/profiles/{id}", d.profileHandler.AdminGetByID)
			r.Get("/admin/resumes", d.resumeHandler.AdminList)
			r.Get("/admin/resumes/{id}", d.resumeHandler.AdminGetByID)

			r.Post("/projects", d.projectHandler.Create)
			r.Put("/projects/{id}", d.projectHandler.Update)
			r.Delete("/projects/{id}", d.projectHandler.Delete)

			r.Post("/articles", d.articleHandler.Create)
			r.Put("/articles/{id}", d.articleHandler.Update)
			r.Delete("/articles/{id}", d.articleHandler.Delete)

			r.Post("/profiles", d.profileHandler.Create)
			r.Put("/profiles/{id}", d.profileHandler.Update)
			r.Delete("/profiles/{id}", d.profileHandler.Delete)

			r.Post("/resumes", d.resumeHandler.Create)
			r.Put("/resumes/{id}", d.resumeHandler.Update)
			r.Delete("/resumes/{id}", d.resumeHandler.Delete)
		})
	})

	return r
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// handleReadyz reports readiness. The database is mandatory: without it the
// probe fails. Valkey is an optimisation, so when it is down the API stays
// ready (reads fall back to PostgreSQL) and the body says the cache is degraded.
func handleReadyz(db, cache pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readyCheckTimeout)
		defer cancel()

		if err := db.Ping(ctx); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}

		if err := cache.Ping(ctx); err != nil {
			slog.WarnContext(r.Context(), "readyz: valkey unreachable", "error", err)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ready (cache degraded)"))
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	}
}

func newServer(handler http.Handler, port string) *http.Server {
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
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http serve: %w", err)
		}
		return nil
	})

	g.Go(func() error {
		<-gctx.Done()
		logger.Info("shutdown started", "timeout", shutdownTimeout.String())

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
