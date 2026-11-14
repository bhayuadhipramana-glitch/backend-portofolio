package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"golang.org/x/sync/errgroup"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/handler"
	authmw "github.com/bhayuadhipramana-glicth/backend-portofolio/internal/middleware"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/platform/github"
	natsbus "github.com/bhayuadhipramana-glicth/backend-portofolio/internal/platform/nats"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/platform/notify"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/platform/postgres"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/platform/valkey"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/usecase"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/worker"
)

const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 10 * time.Second
	// writeTimeout must stay above requestTimeout so a handler that hits its
	// deadline can still write the 503 response before the connection closes.
	writeTimeout   = 30 * time.Second
	requestTimeout = 25 * time.Second
	// Per-IP budget for POST /api/v1/events: each request may carry up to 100
	// events, so this allows a sustained 1,000 events/s per client.
	eventRatePerSecond = 10
	eventBurst         = 20

	// F7 abuse protection: bursts per client IP per minute. The server-wide cap
	// on concurrent bursts is domain.DemoMaxConcurrency.
	demoBurstsPerIP = 2

	// F9 abuse protection: contact submissions per client IP per window.
	contactPerIP  = 3
	contactWindow = 10 * time.Minute

	// SSE guardrails: total concurrent live-feed clients and the share per IP.
	maxStreamClients = 100
	maxStreamsPerIP  = 3

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

	// 3. Event bus: NATS JetStream is required at boot for F3 ingest; once
	// running, an outage makes ingest answer 503 and degrades /readyz.
	natsClient, err := natsbus.Connect(ctx, cfg.natsURL)
	if err != nil {
		return err
	}
	defer natsClient.Close()
	logger.Info("nats connected", "url", cfg.natsURL)

	// 4. Dependency injection: repository -> usecase -> handler.
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

	eventUsecase, err := usecase.NewEventUsecase(natsbus.NewPublisher(natsClient))
	if err != nil {
		return fmt.Errorf("init event usecase: %w", err)
	}

	liveStatsUsecase, err := usecase.NewLiveStatsUsecase(postgres.NewEventStatsRepository(pool))
	if err != nil {
		return fmt.Errorf("init live stats usecase: %w", err)
	}

	// F7 bursts publish through the same F3 usecase. They are bound to ctx, so a
	// shutdown signal stops them, and run in the background of the request.
	demoUsecase, err := usecase.NewDemoBurstUsecase(ctx, eventUsecase, domain.DemoMaxConcurrency)
	if err != nil {
		return fmt.Errorf("init demo burst usecase: %w", err)
	}

	contactUsecase, err := usecase.NewContactUsecase(postgres.NewContactRepository(pool))
	if err != nil {
		return fmt.Errorf("init contact usecase: %w", err)
	}

	benchmarkUsecase, err := usecase.NewBenchmarkUsecase(postgres.NewBenchmarkRepository(pool), contentCache)
	if err != nil {
		return fmt.Errorf("init benchmark usecase: %w", err)
	}

	// 5. Router and handlers.
	router := newRouter(routerDeps{
		db:               pool,
		cache:            contentCache,
		bus:              natsClient,
		appEnv:           cfg.appEnv,
		trustCloudflare:  cfg.trustCloudflare,
		projectHandler:   handler.NewProjectHandler(projectUsecase),
		articleHandler:   handler.NewArticleHandler(articleUsecase),
		profileHandler:   handler.NewProfileHandler(profileUsecase),
		resumeHandler:    handler.NewResumeHandler(resumeUsecase),
		eventHandler:     handler.NewEventHandler(eventUsecase),
		demoHandler:      handler.NewDemoHandler(demoUsecase),
		demoLimiter:      authmw.NewWindowLimiter(demoBurstsPerIP, time.Minute).Middleware,
		contactHandler:   handler.NewContactHandler(contactUsecase),
		contactLimiter:   authmw.NewWindowLimiter(contactPerIP, contactWindow).Middleware,
		benchmarkHandler: handler.NewBenchmarkHandler(benchmarkUsecase),
		benchmarkToken:   cfg.ciBenchmarkToken,
		streamHandler:    handler.NewStreamHandler(liveStatsUsecase, authmw.NewConnLimiter(maxStreamClients, maxStreamsPerIP)),
		eventLimiter:     authmw.NewRateLimiter(eventRatePerSecond, eventBurst).Middleware,
		authHandler:      handler.NewAuthHandler(authUsecase, cfg.appEnv, cfg.successURL, sessionTTL),
		authenticator:    authUsecase,
		allowedOrigins:   cfg.allowedOrigin,
	})

	// 6. F4 aggregation worker, running alongside the HTTP server.
	eventSource, err := natsClient.NewEventSource(ctx)
	if err != nil {
		return err
	}
	aggregator, err := worker.NewAggregator(eventSource, postgres.NewEventRepository(pool), logger)
	if err != nil {
		return fmt.Errorf("init aggregation worker: %w", err)
	}
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		_ = aggregator.Run(ctx) // Run only returns once ctx is cancelled
	}()

	// F9 outbox dispatcher: delivers contact notifications after commit.
	notifier, err := notify.New(cfg.contactWebhookURL, logger)
	if err != nil {
		return err
	}
	dispatcher, err := worker.NewOutboxDispatcher(postgres.NewOutboxRepository(pool), notifier, logger)
	if err != nil {
		return fmt.Errorf("init outbox dispatcher: %w", err)
	}
	outboxDone := make(chan struct{})
	go func() {
		defer close(outboxDone)
		_ = dispatcher.Run(ctx) // Run only returns once ctx is cancelled
	}()

	serveErr := serve(ctx, logger, newServer(router, cfg.port))

	// Shutdown order: stop the HTTP server (done), let the worker finish its
	// in-flight batch, then drain NATS; the deferred pool.Close runs last.
	stop()
	waitCtx, cancelWait := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancelWait()
	if err := demoUsecase.Wait(waitCtx); err != nil {
		logger.Warn("demo bursts did not stop in time", "error", err)
	}
	select {
	case <-workerDone:
	case <-time.After(shutdownTimeout):
		logger.Warn("aggregation worker did not stop in time")
	}
	select {
	case <-outboxDone:
	case <-time.After(shutdownTimeout):
		logger.Warn("outbox dispatcher did not stop in time")
	}
	if err := natsClient.Drain(); err != nil {
		logger.Warn("nats drain failed", "error", err)
	}
	return serveErr
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
	db               pinger
	cache            pinger // Valkey health; an outage degrades readiness, it does not fail it
	bus              pinger // NATS health; an outage degrades readiness, it does not fail it
	appEnv           string
	trustCloudflare  bool
	projectHandler   *handler.ProjectHandler
	articleHandler   *handler.ArticleHandler
	profileHandler   *handler.ProfileHandler
	resumeHandler    *handler.ResumeHandler
	eventHandler     *handler.EventHandler
	demoHandler      *handler.DemoHandler
	demoLimiter      func(http.Handler) http.Handler // per-IP sliding-window limit for demo bursts
	contactHandler   *handler.ContactHandler
	contactLimiter   func(http.Handler) http.Handler // per-IP sliding window for contact submissions
	benchmarkHandler *handler.BenchmarkHandler
	benchmarkToken   string                          // CI bearer token for POST /benchmarks; empty disables it
	streamHandler    *handler.StreamHandler          // F5 SSE live feed, exempt from the request timeout
	eventLimiter     func(http.Handler) http.Handler // per-IP rate limit for event ingest
	authHandler      *handler.AuthHandler
	authenticator    authmw.Authenticator
	allowedOrigins   []string
}

func newRouter(d routerDeps) http.Handler {
	root := chi.NewRouter()

	// CORS goes first so it intercepts preflight requests. Origins must be
	// explicit: with AllowCredentials a wildcard would let any site use the
	// admin session cookie.
	root.Use(cors.Handler(cors.Options{
		AllowedOrigins:   d.allowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link", authmw.CacheHeaderName},
		AllowCredentials: true,
		MaxAge:           300, // seconds a browser may cache the preflight result
	}))

	// Logging and stability middleware. ClientIP replaces chi's RealIP: it only
	// trusts CF-Connecting-IP, and only when running behind Cloudflare Tunnel.
	root.Use(middleware.RequestID)
	root.Use(authmw.ClientIP(d.trustCloudflare))
	root.Use(middleware.Logger)
	root.Use(middleware.Recoverer)

	// F5 live feed: a long-lived SSE stream. It is mounted on the root router,
	// before the Timeout below is attached, because middleware.Timeout (25 s)
	// would cancel it; the handler clears the connection write deadline itself
	// so the server WriteTimeout (30 s) does not cut it either.
	if d.streamHandler != nil {
		root.Get("/api/v1/events/stream", d.streamHandler.Stream)
	}

	// Everything else is a short request and gets the 25 s timeout.
	r := root.With(middleware.Timeout(requestTimeout))

	// Infrastructure routes.
	r.Get("/healthz", handleHealthz)
	r.Get("/readyz", handleReadyz(d.db, d.cache, d.bus))

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

		// F3 public event ingest: per-IP rate limited, body and batch bounded,
		// published to JetStream and aggregated asynchronously by the worker.
		if d.eventHandler != nil {
			r.Group(func(r chi.Router) {
				if d.eventLimiter != nil {
					r.Use(d.eventLimiter)
				}
				r.Post("/events", d.eventHandler.Ingest)
			})
		}

		// F7 public demo burst: strict per-IP window plus a server-wide cap on
		// concurrent bursts (enforced in the usecase), both before NATS is touched.
		if d.demoHandler != nil {
			r.Group(func(r chi.Router) {
				if d.demoLimiter != nil {
					r.Use(d.demoLimiter)
				}
				r.Post("/demo/burst", d.demoHandler.Burst)
			})
		}

		// F9 public contact form: strict per-IP sliding window before any write.
		if d.contactHandler != nil {
			r.Group(func(r chi.Router) {
				if d.contactLimiter != nil {
					r.Use(d.contactLimiter)
				}
				r.Post("/contact", d.contactHandler.Submit)
			})
		}

		// F8 benchmark history: public cached read; publishing needs the admin
		// session or the CI bearer token.
		if d.benchmarkHandler != nil {
			r.With(authmw.CacheStatus).Get("/benchmarks", d.benchmarkHandler.List)
			r.With(authmw.BearerOrAdmin(d.benchmarkToken, trustedOrigin, requireAdmin)).
				Post("/benchmarks", d.benchmarkHandler.Create)
		}

		// Admin-only: unfiltered reads and all mutations.
		r.Group(func(r chi.Router) {
			r.Use(trustedOrigin, requireAdmin)

			if d.contactHandler != nil {
				r.Get("/admin/contacts", d.contactHandler.AdminList)
			}

			// Profiling. Never public. Requests run under the 25 s timeout and the
			// 30 s server WriteTimeout, so use /profile?seconds=20 or less.
			r.Get("/admin/debug/pprof/", pprof.Index)
			r.Get("/admin/debug/pprof/cmdline", pprof.Cmdline)
			r.Get("/admin/debug/pprof/profile", pprof.Profile)
			r.Get("/admin/debug/pprof/symbol", pprof.Symbol)
			r.Post("/admin/debug/pprof/symbol", pprof.Symbol)
			r.Get("/admin/debug/pprof/trace", pprof.Trace)
			r.Get("/admin/debug/pprof/{name}", func(w http.ResponseWriter, r *http.Request) {
				pprof.Handler(chi.URLParam(r, "name")).ServeHTTP(w, r)
			})

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

	return root
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// handleReadyz reports readiness. The database is mandatory: without it the
// probe fails. Valkey and NATS are not: when Valkey is down reads fall back to
// PostgreSQL, and when NATS is down only event ingest fails (503), so the API
// stays ready and the body names what is degraded. A nil pinger is skipped.
func handleReadyz(db, cache, bus pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readyCheckTimeout)
		defer cancel()

		if err := db.Ping(ctx); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}

		var degraded []string
		if cache != nil {
			if err := cache.Ping(ctx); err != nil {
				slog.WarnContext(r.Context(), "readyz: valkey unreachable", "error", err)
				degraded = append(degraded, "cache")
			}
		}
		if bus != nil {
			if err := bus.Ping(ctx); err != nil {
				slog.WarnContext(r.Context(), "readyz: nats unreachable", "error", err)
				degraded = append(degraded, "events")
			}
		}

		w.WriteHeader(http.StatusOK)
		if len(degraded) > 0 {
			_, _ = w.Write([]byte("ready (" + strings.Join(degraded, ", ") + " degraded)"))
			return
		}
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
