// Package audit defines audit log entries.
package audit

import (
	"time"

	"github.com/google/uuid"
)

// Entry is one audit record.
type Entry struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	ActorType    string
	ActorID      string
	ActorLabel   string
	Action       string
	ResourceType string
	ResourceID   string
	Metadata     map[string]any
	RequestID    string
	IP           string
	CreatedAt    time.Time
}

// Well-known actions.
const (
	ActionUserRegistered   = "user.registered"
	ActionUserLogin        = "user.login"
	ActionUserLogout       = "user.logout"
	ActionEmailVerified    = "user.email_verified"
	ActionPasswordReset    = "user.password_reset"
	ActionPasswordChanged  = "user.password_changed"
	ActionIdentityLinked   = "user.identity_linked"
	ActionIdentityUnlinked = "user.identity_unlinked"
	ActionAccountConnected = "social_account.connected"
	ActionAccountRemoved   = "social_account.disconnected"
	ActionAccountExpired   = "social_account.expired"
	ActionPostCreated      = "post.created"
	ActionPostUpdated      = "post.updated"
	ActionPostDeleted      = "post.deleted"
	ActionPostScheduled    = "post.scheduled"
	ActionPostUnscheduled  = "post.unscheduled"
	ActionPostCancelled    = "post.cancelled"
	ActionPostPublishReq   = "post.publish_requested"
	ActionPostRetried      = "post.retried"
	ActionTargetPublished  = "post_target.published"
	ActionTargetFailed     = "post_target.failed"
	ActionTargetReview     = "post_target.needs_review"
	ActionPostCompleted    = "post.completed"
	ActionMediaUploaded    = "media.uploaded"
	ActionMediaDeleted     = "media.deleted"
	ActionAPIKeyCreated    = "api_key.created"
	ActionAPIKeyRevoked    = "api_key.revoked"
	ActionMCPCreated       = "mcp_connection.created"
	ActionMCPRevoked       = "mcp_connection.revoked"
	// ActionAgentSignupConfirmed: the owner confirmed an agent sign-up and its key started working (D-025).
	ActionAgentSignupConfirmed = "agent_signup.confirmed"
	ActionAPIRequest           = "api_key.request"
	ActionApprovalRequested    = "approval.requested"
	ActionApprovalApproved     = "approval.approved"
	ActionApprovalDenied       = "approval.denied"
	ActionApprovalUsed         = "approval.used"
	ActionExportRequested      = "account.export_requested"
	ActionExportReady          = "account.export_ready"
	ActionExportFailed         = "account.export_failed"
	ActionExportDownloaded     = "account.export_downloaded"
	ActionDeletionScheduled    = "account.deletion_scheduled"
	ActionDeletionCancelled    = "account.deletion_cancelled"
	ActionMCPToolCall          = "mcp.tool_call" // one MCP tool call made with an API key (X-MCP-Tool header)
)
