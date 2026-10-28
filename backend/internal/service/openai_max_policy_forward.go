package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type OpenAIMaxBlockedError struct{ Message string }

func (e *OpenAIMaxBlockedError) Error() string { return e.Message }

// Apply before protocol conversion, which can fold max into xhigh on older
// models. Only explicit effort fields are controlled; xhigh remains distinct.
func (s *OpenAIGatewayService) applyOpenAIMaxPolicyToBody(ctx context.Context, account *Account, body []byte) ([]byte, error) {
	if account == nil || account.Platform != PlatformOpenAI || s.settingService == nil {
		return body, nil
	}
	var paths []string
	effortPaths := []string{"reasoning.effort", "reasoning_effort", "output_config.effort"}
	switch gjson.GetBytes(body, "type").String() {
	case "response.create":
		effortPaths = append(effortPaths, "response.reasoning.effort")
	case "session.update":
		effortPaths = append(effortPaths, "session.reasoning.effort")
	}
	for _, path := range effortPaths {
		effort := gjson.GetBytes(body, path)
		if effort.Type == gjson.String && strings.EqualFold(strings.TrimSpace(effort.String()), "max") {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return body, nil
	}
	settings, err := s.settingService.GetOpenAIMaxPolicySettings(ctx)
	if err != nil {
		return body, err
	}
	if (settings.Scope == BetaPolicyScopeOAuth && !account.IsOpenAIOAuthLike()) ||
		(settings.Scope == BetaPolicyScopeAPIKey && account.Type != AccountTypeAPIKey) {
		return body, nil
	}
	switch settings.Action {
	case BetaPolicyActionBlock:
		message := strings.TrimSpace(settings.ErrorMessage)
		if message == "" {
			message = "OpenAI Max effort is not allowed"
		}
		return body, &OpenAIMaxBlockedError{Message: message}
	case OpenAIMaxPolicyActionDowngrade:
		for _, path := range paths {
			body, err = sjson.SetBytes(body, path, settings.DowngradeEffort)
			if err != nil {
				return nil, fmt.Errorf("downgrade openai max effort: %w", err)
			}
		}
	}
	return body, nil
}

func (s *OpenAIGatewayService) applyOpenAIMaxPolicyForHTTP(ctx context.Context, c *gin.Context, account *Account, body []byte) ([]byte, error) {
	body, err := s.applyOpenAIMaxPolicyToBody(ctx, account, body)
	var blocked *OpenAIMaxBlockedError
	if errors.As(err, &blocked) {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		committed := StopOpenAICompactSSEKeepaliveCommitted(c)
		if ctx.Err() != nil {
			return body, err
		}
		MarkResponseCommitted(c)
		path := c.Request.URL.Path
		if committed || c.Writer.Written() {
			switch {
			case strings.Contains(path, "/responses"):
				writeOpenAICompactSSEFailureMessage(c, http.StatusBadRequest, "invalid_request_error", blocked.Message)
			case strings.HasSuffix(path, "/messages"):
				MarkOpsStreamError(c, "invalid_request_error", blocked.Message, http.StatusBadRequest)
				_, _ = c.Writer.WriteString(buildAnthropicStreamErrorSSE("invalid_request_error", blocked.Message))
				c.Writer.Flush()
			default:
				MarkOpsStreamError(c, "invalid_request_error", blocked.Message, http.StatusBadRequest)
				_, _ = c.Writer.WriteString(buildChatStreamErrorSSE("policy_violation", blocked.Message))
				c.Writer.Flush()
			}
		} else if strings.HasSuffix(path, "/messages") {
			writeAnthropicError(c, http.StatusBadRequest, "invalid_request_error", blocked.Message)
		} else {
			writeChatCompletionsError(c, http.StatusBadRequest, "invalid_request_error", blocked.Message)
		}
	}
	return body, err
}

// Both WS transports call this on each response.create, after group effort policy.
func (s *OpenAIGatewayService) applyOpenAIMaxPolicyForWS(ctx context.Context, c *gin.Context, conn *coderws.Conn, account *Account, body []byte) ([]byte, error) {
	frameType := gjson.GetBytes(body, "type").String()
	if frameType != "response.create" && frameType != "session.update" {
		return body, nil
	}
	body, err := s.applyOpenAIMaxPolicyToBody(ctx, account, body)
	var blocked *OpenAIMaxBlockedError
	if errors.As(err, &blocked) {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
		payload, _ := json.Marshal(gin.H{
			"type": "error", "event_id": newOpenAIFastPolicyWSEventID(),
			"error": gin.H{"type": "invalid_request_error", "code": "policy_violation", "message": blocked.Message},
		})
		writeCtx, cancel := context.WithTimeout(ctx, s.openAIWSWriteTimeout())
		defer cancel()
		_ = conn.Write(writeCtx, coderws.MessageText, payload)
		return body, NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, blocked.Message, blocked)
	}
	return body, err
}
