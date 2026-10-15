package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/socialos/backend/internal/application/port"
	"github.com/socialos/backend/internal/application/scheduler"
)

// ServerConfig configures the worker server.
type ServerConfig struct {
	Queue       string
	Concurrency int
	// DelayedCheck is how often scheduled tasks are promoted (default 1s, so a post goes out within ~1s of its time).
	DelayedCheck time.Duration
	// ShutdownTimeout is how long Shutdown waits for in-flight publish tasks before aborting them (default 30s).
	ShutdownTimeout time.Duration
	// ExportShutdownTimeout is the same for the export server (default 5s). A build cut short is recorded as
	// interrupted and the user asks again, so it is not worth a long wait inside the stop_grace_period.
	ExportShutdownTimeout time.Duration
	// RetryDelay overrides the retry backoff (default scheduler.RetryDelay: 30s·2^n ±20%). Tests only.
	RetryDelay func(n int, err error) time.Duration
	// Mailer, when set, makes this worker deliver mail:send tasks.
	Mailer port.Mailer
	// Auth, when set, makes this worker run the password-reset lookup and retire the links of undelivered mail.
	Auth AuthTasks
	// Exports, when set, makes this worker build data exports.
	Exports ExportTasks
	// Purge, when set, makes this worker delete accounts whose grace period is over.
	Purge PurgeTasks
}

// PurgeTasks is what the worker needs from the account deletion service.
type PurgeTasks interface {
	Purge(ctx context.Context, userID uuid.UUID) error
}

// ExportTasks is what the worker needs from the export service.
type ExportTasks interface {
	Build(ctx context.Context, exportID uuid.UUID) error
}

// AuthTasks is what the worker needs from the auth service.
type AuthTasks interface {
	ProcessForgot(ctx context.Context, email string) error
	ProcessAgentSignup(ctx context.Context, id uuid.UUID) error
	RetireMailToken(ctx context.Context, tokenID string) error
}

// Server runs publish handlers.
type Server struct {
	srv *asynq.Server
	mux *asynq.ServeMux
	// exports runs data exports on their own queue with one worker (nil without ServerConfig.Exports).
	exports *asynq.Server
	exMux   *asynq.ServeMux
}

// NewServer wires the publisher into an Asynq server.
func NewServer(redis asynq.RedisConnOpt, cfg ServerConfig, pub *scheduler.Publisher, log *slog.Logger) *Server {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 10
	}
	if cfg.DelayedCheck <= 0 {
		cfg.DelayedCheck = time.Second
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = 30 * time.Second
	}
	if cfg.ExportShutdownTimeout <= 0 {
		cfg.ExportShutdownTimeout = 5 * time.Second
	}
	retryDelay := cfg.RetryDelay
	if retryDelay == nil {
		retryDelay = scheduler.RetryDelay
	}
	srv := asynq.NewServer(redis, asynq.Config{
		Concurrency:              cfg.Concurrency,
		Queues:                   map[string]int{cfg.Queue: 1},
		RetryDelayFunc:           func(n int, err error, _ *asynq.Task) time.Duration { return retryDelay(n, err) },
		ShutdownTimeout:          cfg.ShutdownTimeout,
		DelayedTaskCheckInterval: cfg.DelayedCheck,
		Logger:                   asynqLogger{log: log},
		ErrorHandler: asynq.ErrorHandlerFunc(func(ctx context.Context, t *asynq.Task, err error) {
			retried, _ := asynq.GetRetryCount(ctx)
			log.WarnContext(ctx, "publish task returned error", slog.String("type", t.Type()), slog.Int("retried", retried), slog.Any("error", err))
		}),
	})
	mux := asynq.NewServeMux()
	mux.HandleFunc(TypePublishTarget, Handler(pub))
	if cfg.Mailer != nil {
		var retire func(context.Context, string) error
		if cfg.Auth != nil {
			retire = cfg.Auth.RetireMailToken
		}
		mux.HandleFunc(TypeMailSend, MailHandler(cfg.Mailer, retire))
	}
	if cfg.Auth != nil {
		mux.HandleFunc(TypeAuthForgot, ForgotHandler(cfg.Auth.ProcessForgot))
		mux.HandleFunc(TypeAgentSignup, AgentSignupHandler(cfg.Auth.ProcessAgentSignup))
	}
	s := &Server{srv: srv, mux: mux}
	if cfg.Exports != nil || cfg.Purge != nil {
		s.exports = asynq.NewServer(redis, asynq.Config{
			Concurrency: 1, Queues: map[string]int{ExportQueueName(cfg.Queue): 1}, ShutdownTimeout: cfg.ExportShutdownTimeout,
			Logger: asynqLogger{log: log},
			ErrorHandler: asynq.ErrorHandlerFunc(func(ctx context.Context, t *asynq.Task, err error) {
				log.WarnContext(ctx, "export task returned error", slog.String("type", t.Type()), slog.Any("error", err))
			}),
		})
		s.exMux = asynq.NewServeMux()
		if cfg.Exports != nil {
			s.exMux.HandleFunc(TypeAccountExport, ExportHandler(cfg.Exports.Build))
		}
		// Purges run on the same maintenance queue, so a long purge never occupies a publishing slot either.
		if cfg.Purge != nil {
			s.exMux.HandleFunc(TypeAccountPurge, PurgeHandler(cfg.Purge.Purge))
		}
	}
	return s
}

// Handler adapts the publisher to an Asynq handler.
func Handler(pub *scheduler.Publisher) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		var pl scheduler.Payload
		if err := json.Unmarshal(t.Payload(), &pl); err != nil {
			return fmt.Errorf("bad payload: %v: %w", err, asynq.SkipRetry)
		}
		retried, _ := asynq.GetRetryCount(ctx)
		maxRetry, ok := asynq.GetMaxRetry(ctx)
		if !ok {
			maxRetry = scheduler.MaxRetry
		}
		return pub.Run(ctx, pl, scheduler.RetryInfo{Retried: retried, MaxRetry: maxRetry})
	}
}

// Start begins processing in the background.
func (s *Server) Start() error {
	if err := s.srv.Start(s.mux); err != nil {
		return err
	}
	if s.exports != nil {
		return s.exports.Start(s.exMux)
	}
	return nil
}

// Shutdown stops both servers from taking new tasks at once, then waits for in-flight ones concurrently: the publish
// server up to ShutdownTimeout, the export server up to ExportShutdownTimeout. The total is the larger of the two, not
// their sum, so it fits the stop_grace_period. Unfinished tasks are aborted and returned to the queue.
func (s *Server) Shutdown() {
	s.srv.Stop()
	if s.exports != nil {
		s.exports.Stop()
	}
	var wg sync.WaitGroup
	if s.exports != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.exports.Shutdown()
		}()
	}
	s.srv.Shutdown()
	wg.Wait()
}

type asynqLogger struct{ log *slog.Logger }

func (l asynqLogger) Debug(args ...any) { l.log.Debug(fmt.Sprint(args...)) }
func (l asynqLogger) Info(args ...any)  { l.log.Info(fmt.Sprint(args...)) }
func (l asynqLogger) Warn(args ...any)  { l.log.Warn(fmt.Sprint(args...)) }
func (l asynqLogger) Error(args ...any) { l.log.Error(fmt.Sprint(args...)) }
func (l asynqLogger) Fatal(args ...any) { l.log.Error(fmt.Sprint(args...)) }
