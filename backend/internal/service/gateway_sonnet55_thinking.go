package service

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// Match the existing OAuth forwarding order: mimicry expands short model IDs
// before account mapping, while real Claude Code requests bypass mimicry.
func claudeOAuthModelForValidation(ctx context.Context, c *gin.Context, account *Account, parsed *ParsedRequest, countTokens bool) string {
	model := parsed.Model
	if normalized := claude.NormalizeModelID(model); normalized != model {
		userAgent := ""
		if c != nil {
			userAgent = c.GetHeader("User-Agent")
		}
		isClaudeCode := IsClaudeCodeClient(ctx) || isClaudeCodeClient(userAgent, parsed.MetadataUserID)
		// Only the messages path recognizes proxied Claude Code billing headers.
		if !countTokens && parsed.MetadataUserID != "" && systemHasBillingAttributionBlock(parsed.Body.Bytes()) {
			isClaudeCode = true
		}
		if !isClaudeCode {
			model = normalized
		}
	}
	return account.GetMappedModel(model)
}

// Preserve an explicit no-up-front-thinking request on Sonnet 5.5. Keep other
// fields intact so validation still rejects incompatible effort and options.
func normalizeClaude55Thinking(body []byte, model string) ([]byte, bool) {
	if !claude.IsSonnet55(model) || gjson.GetBytes(body, "thinking.type").String() != "disabled" {
		return body, false
	}
	return setJSONValueBytes(body, "thinking.type", "between_tools")
}
