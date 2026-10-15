// Package mail contains the Mailer adapters (SMTP, log) and the message templates.
package mail

import (
	"context"
	"log/slog"

	"github.com/socialos/backend/internal/application/port"
	"github.com/socialos/backend/internal/domain/user"
)

// Log is a Mailer that writes to the log instead of sending. Bodies carry
// one-time links, so they are logged only in development.
type Log struct {
	log     *slog.Logger
	logBody bool
}

// NewLog builds the log mailer; env is APP_ENV.
func NewLog(log *slog.Logger, env string) *Log {
	return &Log{log: log, logBody: env == "development"}
}

// Send logs the message with a masked recipient.
func (l *Log) Send(ctx context.Context, m port.Message) error {
	attrs := []any{slog.String("to", user.MaskEmail(m.To)), slog.String("subject", m.Subject), slog.String("template", m.Template)}
	if l.logBody {
		attrs = append(attrs, slog.String("text", m.Text))
	}
	l.log.InfoContext(ctx, "mail not sent (MAIL_PROVIDER=log)", attrs...)
	return nil
}
