package tools

import (
	"context"

	"github.com/charmbracelet/crush/internal/lsp"
)

type (
	sessionIDContextKey string
	messageIDContextKey string
	supportsImagesKey   string
	modelNameKey        string
	lspBatcherKey       string
)

const (
	// SessionIDContextKey is the key for the session ID in the context.
	SessionIDContextKey sessionIDContextKey = "session_id"
	// MessageIDContextKey is the key for the message ID in the context.
	MessageIDContextKey messageIDContextKey = "message_id"
	// SupportsImagesContextKey is the key for the model's image support capability.
	SupportsImagesContextKey supportsImagesKey = "supports_images"
	// ModelNameContextKey is the key for the model name in the context.
	ModelNameContextKey modelNameKey = "model_name"
	// LSPBatcherContextKey is the key for the LSP batcher in the context.
	LSPBatcherContextKey lspBatcherKey = "lsp_batcher"
)

// GetSessionFromContext retrieves the session ID from the context.
func GetSessionFromContext(ctx context.Context) string {
	sessionID := ctx.Value(SessionIDContextKey)
	if sessionID == nil {
		return ""
	}
	s, ok := sessionID.(string)
	if !ok {
		return ""
	}
	return s
}

// GetMessageFromContext retrieves the message ID from the context.
func GetMessageFromContext(ctx context.Context) string {
	messageID := ctx.Value(MessageIDContextKey)
	if messageID == nil {
		return ""
	}
	s, ok := messageID.(string)
	if !ok {
		return ""
	}
	return s
}

// GetSupportsImagesFromContext retrieves whether the model supports images from the context.
func GetSupportsImagesFromContext(ctx context.Context) bool {
	supportsImages := ctx.Value(SupportsImagesContextKey)
	if supportsImages == nil {
		return false
	}
	if supports, ok := supportsImages.(bool); ok {
		return supports
	}
	return false
}

// GetModelNameFromContext retrieves the model name from the context.
func GetModelNameFromContext(ctx context.Context) string {
	modelName := ctx.Value(ModelNameContextKey)
	if modelName == nil {
		return ""
	}
	s, ok := modelName.(string)
	if !ok {
		return ""
	}
	return s
}

// GetLSPBatcherFromContext retrieves the LSP batcher from the context.
func GetLSPBatcherFromContext(ctx context.Context) *lsp.Batcher {
	batcher := ctx.Value(LSPBatcherContextKey)
	if batcher == nil {
		return nil
	}
	b, ok := batcher.(*lsp.Batcher)
	if !ok {
		return nil
	}
	return b
}

// WithLSPBatcher adds an LSP batcher to the context.
func WithLSPBatcher(ctx context.Context, batcher *lsp.Batcher) context.Context {
	return context.WithValue(ctx, LSPBatcherContextKey, batcher)
}
