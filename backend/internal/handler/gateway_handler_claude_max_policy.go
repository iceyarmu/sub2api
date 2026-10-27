package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// handleClaudeMaxPolicyError preserves the endpoint's JSON format before any
// bytes are sent, and emits a terminal SSE error after a slot-wait heartbeat.
func (h *GatewayHandler) handleClaudeMaxPolicyError(c *gin.Context, err error, streamStarted bool, writeJSON func(*gin.Context, int, string, string)) bool {
	var blocked *service.ClaudeMaxBlockedError
	if !errors.As(err, &blocked) {
		return false
	}
	service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalPolicyDenied)
	if errors.Is(c.Request.Context().Err(), context.Canceled) {
		failoverClientGone(c)
		return true
	}
	if streamStarted || c.Writer.Written() {
		h.handleStreamingAwareError(c, http.StatusBadRequest, "invalid_request_error", blocked.Message, true)
	} else {
		writeJSON(c, http.StatusBadRequest, "invalid_request_error", blocked.Message)
	}
	return true
}
