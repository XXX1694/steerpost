// Package app is the composition root: it wires configuration, infrastructure,
// adapters and use cases for the api and worker binaries (and E2E tests).
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/socialos/backend/internal/adapters/bluesky"
	"github.com/socialos/backend/internal/adapters/discord"
	"github.com/socialos/backend/internal/adapters/linkedin"
	"github.com/socialos/backend/internal/adapters/mail"
	"github.com/socialos/backend/internal/adapters/mastodon"
	"github.com/socialos/backend/internal/adapters/mock"
	"github.com/socialos/backend/internal/adapters/mocktoken"
	"github.com/socialos/backend/internal/adapters/provider"
	"github.com/socialos/backend/internal/adapters/stubs"
	"github.com/socialos/backend/internal/adapters/telegram"
	"github.com/socialos/backend/internal/application/account"
	"github.com/socialos/backend/internal/application/accounts"
	"github.com/socialos/backend/internal/application/analytics"
	"github.com/socialos/backend/internal/application/approvals"
	"github.com/socialos/backend/internal/application/audit"
	"github.com/socialos/backend/internal/application/auth"
	"github.com/socialos/backend/internal/application/developer"
	"github.com/socialos/backend/internal/application/media"
	"github.com/socialos/backend/internal/application/port"
	"github.com/socialos/backend/internal/application/posts"
	"github.com/socialos/backend/internal/application/quota"
	"github.com/socialos/backend/internal/application/scheduler"
	"github.com/socialos/backend/internal/config"
	"github.com/socialos/backend/internal/infrastructure/clock"
	"github.com/socialos/backend/internal/infrastructure/crypto"
	"github.com/socialos/backend/internal/infrastructure/postgres"
	"github.com/socialos/backend/internal/infrastructure/queue"
	"github.com/socialos/backend/internal/infrastructure/redis"
	"github.com/socialos/backend/internal/infrastructure/storage"
	"github.com/socialos/backend/internal/observability"
	transport "github.com/socialos/backend/internal/transport/http"
	"github.com/socialos/backend/internal/transport/middleware"
)

// Overrides let tests replace infrastructure pieces.
type Overrides struct {
	Storage   media.Storage
	Clock     port.Clock
	Providers []provider.Provider
	Hasher    auth.PasswordHasher
	// Mailer replaces the configured mail adapter (tests).
	Mailer port.Mailer
	// SignIn replaces the social sign-in providers built from configuration (tests point them at fakes).
	SignIn []auth.IdentityProvider
}

// App holds every wired component.
type App struct {
	Cfg        *config.Config
	Log        *slog.Logger
	DB         *postgres.DB
	Redis      *redis.Conn
	Queue      *queue.Client
	Mailer     port.Mailer    // sends mail; used by the worker
	MailQueue  port.MailQueue // enqueues mail; used by the API
	Storage    media.Storage
	Registry   *provider.Registry
	Metrics    *observability.Metrics
	Services   transport.Services
	Publisher  *scheduler.Publisher
	Reconciler *scheduler.Reconciler
	APILimiter *middleware.Limiter
	// AgentLimit caps API-key requests per user (nil when QUOTA_AGENT_RPM=-1).
	AgentLimit *middleware.Limiter
	AuthLimit  *middleware.Limiter
	MailLimit  *middleware.Limiter
}

// Build connects to dependencies and wires the application.
func Build(ctx context.Context, cfg *config.Config, log *slog.Logger, ov Overrides) (*App, error) {
	a := &App{Cfg: cfg, Log: log, Metrics: observability.NewMetrics()}
	var err error
	if a.DB, err = postgres.Open(ctx, cfg.DatabaseURL, cfg.DBMaxConns); err != nil {
		return nil, err
	}
	if a.Redis, err = redis.Open(cfg.RedisURL); err != nil {
		a.Close()
		return nil, err
	}
	a.Queue = queue.NewClient(a.Redis.Asynq, cfg.QueueName)
	if a.Storage, err = buildStorage(ctx, cfg, ov.Storage); err != nil {
		a.Close()
		return nil, err
	}
	if a.Mailer, err = buildMailer(cfg, log, ov.Mailer); err != nil {
		a.Close()
		return nil, err
	}
	a.MailQueue = a.Queue.MailQueue()
	a.Registry = buildRegistry(cfg, ov.Providers)
	if err := a.wire(cfg, log, ov); err != nil {
		a.Close()
		return nil, err
	}
	return a, nil
}

func buildStorage(ctx context.Context, cfg *config.Config, override media.Storage) (media.Storage, error) {
	if override != nil {
		return override, nil
	}
	if cfg.StorageDriver == "memory" {
		return storage.NewMemory(), nil
	}
	return storage.NewS3(ctx, storage.S3Config{Endpoint: cfg.S3Endpoint, AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey,
		Bucket: cfg.S3Bucket, Region: cfg.S3Region, UseSSL: cfg.S3UseSSL, PublicEndpoint: cfg.S3PublicURL, AutoCreate: cfg.S3AutoCreate})
}

func buildMailer(cfg *config.Config, log *slog.Logger, override port.Mailer) (port.Mailer, error) {
	if override != nil {
		return override, nil
	}
	if cfg.MailProvider != config.MailProviderSMTP {
		return mail.NewLog(log, cfg.Env), nil
	}
	return mail.NewSMTP(mail.SMTPConfig{Host: cfg.SMTPHost, Port: cfg.SMTPPort, TLS: cfg.SMTPTLS,
		Username: cfg.SMTPUsername, Password: cfg.SMTPPassword, From: cfg.MailFrom})
}

// requireVerification: unverified owners are restricted only when mail can really be
// delivered. With MAIL_PROVIDER=log nobody could ever receive the link.
func requireVerification(cfg *config.Config) bool { return cfg.MailProvider == config.MailProviderSMTP }

func mailDelivery(cfg *config.Config) string {
	if cfg.MailProvider == config.MailProviderSMTP {
		return config.MailProviderSMTP
	}
	return config.MailProviderLog
}

func buildRegistry(cfg *config.Config, extra []provider.Provider) *provider.Registry {
	reg := provider.NewRegistry(stubs.All()...)
	reg.Register(linkedin.New(linkedin.Config{ClientID: cfg.LinkedInID, ClientSecret: cfg.LinkedInSecret,
		APIVersion: cfg.LinkedInVersion, UsePKCE: cfg.LinkedInPKCE}))
	reg.Register(telegram.New(telegram.Config{BotToken: cfg.TelegramToken}))
	reg.Register(discord.New(discord.Config{}))
	reg.Register(mastodon.New(mastodon.Config{}))
	reg.Register(bluesky.New(bluesky.Config{}))
	if cfg.MockProviders {
		reg.Register(mock.New())
		reg.Register(mocktoken.New())
	}
	for _, p := range extra {
		reg.Register(p)
	}
	return reg
}

func (a *App) wire(cfg *config.Config, log *slog.Logger, ov Overrides) error {
	clk := ov.Clock
	if clk == nil {
		clk = clock.System{}
	}
	enc, err := crypto.NewCipherFromBase64(cfg.EncryptionKey)
	if err != nil {
		return err
	}
	hasher := ov.Hasher
	if hasher == nil {
		hasher = crypto.NewPasswordHasher(crypto.DefaultArgon2, cfg.PasswordHashConcurrency, int64(cfg.PasswordHashMemoryMiB)*1024)
	}
	db := a.DB
	auditRepo := postgres.NewAudit(db)
	auditSvc := audit.NewService(auditRepo, clk)
	accountRepo, postRepo, jobRepo := postgres.NewAccounts(db), postgres.NewPosts(db), postgres.NewJobs(db)
	mediaRepo, keyRepo, analyticsRepo := postgres.NewMedia(db), postgres.NewAPIKeys(db), postgres.NewAnalytics(db)

	social, err := buildSignIn(cfg, db, enc, ov.SignIn)
	if err != nil {
		return err
	}
	approvalSvc := approvals.NewService(approvals.Deps{Repo: postgres.NewApprovals(db), Tx: db, Audit: auditSvc, Clock: clk,
		Config: approvals.Config{TTL: cfg.ApprovalTTL, MaxPending: cfg.ApprovalMaxPending, WebBaseURL: cfg.WebBaseURL}})
	authSvc, err := auth.NewService(auth.Deps{Users: postgres.NewUsers(db), Sessions: postgres.NewSessions(db), APIKeys: keyRepo,
		Hasher: hasher, Tx: db, Audit: auditSvc, Clock: clk, SessionTTL: cfg.SessionTTL,
		Tokens: postgres.NewEmailTokens(db), Mail: a.MailQueue, Forgot: a.Queue.ForgotQueue(), Log: log, WebBaseURL: cfg.WebBaseURL,
		RequireVerification: requireVerification(cfg), Social: social, AgentSignup: buildAgentSignup(db, a.Queue, keyRepo)})
	if err != nil {
		return fmt.Errorf("auth service: %w", err)
	}
	fingerprintKey, err := crypto.Subkey(cfg.EncryptionKey, "approval-fingerprint")
	if err != nil {
		return fmt.Errorf("approval fingerprint key: %w", err)
	}
	quotaSvc := quota.NewService(postgres.NewQuota(db), cfg.QuotaLimits(), clk)
	postSvc := posts.NewService(posts.Deps{Repo: postRepo, Jobs: jobRepo, Queue: a.Queue, Accounts: accountRepo, Media: mediaRepo,
		Registry: a.Registry, Tx: db, Audit: auditSvc, Clock: clk, Log: log, Gate: approvalSvc, Quota: quotaSvc,
		MinAgentLead: cfg.AgentMinScheduleLead, NoAgentLead: cfg.AgentMinScheduleLead == 0})
	accountSvc := accounts.NewService(accounts.Deps{Repo: accountRepo, States: postgres.NewOAuthStates(db), Links: postgres.NewLinkCodes(db),
		Log: log, Registry: a.Registry, Tx: db, Audit: auditSvc, Clock: clk, Enc: enc, RedirectBaseURL: cfg.APIPublicURL,
		Gate: verifiedOwners{users: postgres.NewUsers(db), enforce: requireVerification(cfg)}, Approvals: approvalSvc,
		FingerprintKey: fingerprintKey, Quota: quotaSvc})
	analyticsSvc := analytics.NewService(analyticsRepo, clk)
	a.Services = transport.Services{
		Auth: authSvc, Accounts: accountSvc, Audit: auditSvc, Analytics: analyticsSvc, Approvals: approvalSvc, Quota: quotaSvc,
		Exports: account.NewExportService(account.Deps{Exports: postgres.NewExports(db), Data: postgres.NewExportData(db),
			Store: a.Storage, Queue: a.Queue.ExportQueue(), Tx: db, Audit: auditSvc, Clock: clk, Log: log,
			Retention: cfg.ExportRetention()}),
		Posts: postSvc,
		Deletion: account.NewDeletionService(account.DeletionDeps{Repo: postgres.NewDeletions(db), Users: postgres.NewUsers(db),
			Passwords: hasher, Sessions: postgres.NewSessions(db), Keys: keyRepo, Posts: postSvc, Queue: a.Queue.PurgeQueue(),
			Store: a.Storage, Prefixes: prefixDeleter(a.Storage), Mail: a.MailQueue, Tx: db, Audit: auditSvc, Clock: clk, Log: log, WebBaseURL: cfg.WebBaseURL,
			Grace: cfg.DeletionGrace()}),
		Media: media.NewService(mediaRepo, a.Storage, auditSvc, clk,
			media.WithUploadLimit(cfg.MediaUploadConcurrency, media.DefaultUploadWait)).WithQuota(quotaSvc, db),
		Developer: developer.NewService(developer.Deps{Keys: keyRepo, Connections: postgres.NewMCPConnections(db), Usage: auditRepo,
			Tx: db, Audit: auditSvc, Clock: clk, MCPPublicURL: cfg.MCPPublicURL, APIPublicURL: cfg.APIPublicURL}),
	}
	a.Publisher = scheduler.NewPublisher(scheduler.Deps{Owners: postgres.NewUsers(db), Unscheduler: postSvc, Targets: postRepo, Posts: postRepo, Jobs: jobRepo,
		Accounts: accountsAdapter{repo: accountRepo, svc: accountSvc}, Vault: accountSvc.Vault(),
		Media: mediaAdapter{repo: mediaRepo, storage: a.Storage}, Metrics: analyticsSvc, Registry: a.Registry,
		Tx: db, Audit: auditSvc, Clock: clk, Log: log, Queue: a.Queue,
		OnOutcome: func(prov, outcome string) { a.Metrics.PublishOutcomes.WithLabelValues(prov, outcome).Inc() }})
	a.Reconciler = scheduler.NewReconciler(a.Publisher, a.Queue)
	a.APILimiter = middleware.NewLimiter(cfg.RateLimitRPS, cfg.RateLimitBurst)
	a.AuthLimit = middleware.NewLimiter(cfg.AuthRateRPS, cfg.AuthRateBurst)
	a.MailLimit = middleware.NewLimiter(1.0/60, 3)
	if rpm := cfg.QuotaLimits().AgentRPM; rpm > 0 {
		a.AgentLimit = middleware.NewLimiter(float64(rpm)/60, rpm)
	}
	return nil
}

// NewTelegramPoller builds the getUpdates poller for TELEGRAM_UPDATES_MODE=polling.
// It returns nil when the registered Telegram provider cannot poll (not configured).
// Every poller shares one lease and offset in Redis, so any number of workers may
// run it while only one actually polls.
func (a *App) NewTelegramPoller(o telegram.PollerOptions) *telegram.Poller {
	p, err := a.Registry.Get(telegram.Name)
	src, ok := p.(telegram.UpdateSource)
	if err != nil || !ok || !p.Configured() {
		return nil
	}
	prefix := "socialos:telegram:" + a.Cfg.QueueName + ":"
	store := telegramPollStore{redis.NewPollState(a.Redis.Client, prefix)}
	handle := func(ctx context.Context, raw []byte) error {
		return a.Services.Accounts.HandleChatUpdate(ctx, telegram.Name, raw)
	}
	return telegram.NewPoller(src, store, handle, a.Log, o)
}

// Router returns the HTTP handler.
func (a *App) Router() http.Handler {
	webhookSecret := ""
	if a.Cfg.TelegramUpdatesMode == config.TelegramModeWebhook {
		webhookSecret = a.Cfg.TelegramWebhookSecret
	}
	return transport.NewRouter(a.Services, transport.Options{
		WebBaseURL: a.Cfg.WebBaseURL, CORSOrigins: a.Cfg.CORSOrigins, CookieSecure: a.Cfg.CookieSecure,
		CookieDomain: a.Cfg.CookieDomain, TrustedProxies: a.Cfg.TrustedProxies, MetricsToken: a.Cfg.MetricsToken, UploadMinKBps: a.Cfg.UploadMinKBps, GatewaySecret: a.Cfg.GatewaySecret,
		Logger: a.Log, Metrics: a.Metrics, APILimiter: a.APILimiter, AuthLimiter: a.AuthLimit, AgentLimiter: a.AgentLimit,
		MailLimiter: a.MailLimit, MailDelivery: mailDelivery(a.Cfg), DeletionGraceDays: a.Cfg.DeletionGraceDays, RequireVerification: requireVerification(a.Cfg),
		TelegramWebhookSecret: webhookSecret,
		Ready: []transport.ReadyCheck{
			{Name: "postgres", Check: a.DB.Ping},
			{Name: "redis", Check: a.Redis.Ping},
			{Name: "storage", Check: a.Storage.Ping},
		},
	})
}

// Close releases connections.
func (a *App) Close() {
	if a.Queue != nil {
		_ = a.Queue.Close()
	}
	if a.Redis != nil {
		_ = a.Redis.Close()
	}
	if a.DB != nil {
		a.DB.Close()
	}
}

// prefixDeleter returns the storage's prefix deletion (S3 and the in-memory store have it); nil for a test double
// that cannot list objects.
func prefixDeleter(s media.Storage) account.PrefixDeleter {
	pd, _ := s.(account.PrefixDeleter)
	return pd
}
