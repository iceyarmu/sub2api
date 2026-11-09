package service

import (
	"context"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func supportsGroupModelMapping(platform string) bool {
	return platform == PlatformOpenAI || platform == PlatformAnthropic
}

func normalizeGroupModelMapping(platform string, raw map[string]string) (map[string]string, error) {
	if !supportsGroupModelMapping(platform) || len(raw) == 0 {
		return map[string]string{}, nil
	}
	mapping := make(map[string]string, len(raw))
	for source, target := range raw {
		source, target = strings.TrimSpace(source), strings.TrimSpace(target)
		if source == "" || target == "" || strings.Contains(strings.TrimSuffix(source, "*"), "*") || strings.Contains(target, "*") {
			return nil, infraerrors.BadRequest("INVALID_GROUP_MODEL_MAPPING", "model mapping requires non-empty models; only a trailing * in the source is supported")
		}
		if _, exists := mapping[source]; exists {
			return nil, infraerrors.BadRequest("INVALID_GROUP_MODEL_MAPPING", "duplicate source model after trimming whitespace")
		}
		mapping[source] = target
	}
	return mapping, nil
}

// applyGroupModelMapping shares the existing pre-account forwarding path without
// requiring a channel. A group rule matches the original request and overrides
// a channel rule; channel metadata is retained for usage and pricing.
func applyGroupModelMapping(ctx context.Context, groupID int64, model string, mapping ChannelMappingResult) ChannelMappingResult {
	group, _ := ctx.Value(ctxkey.Group).(*Group)
	if group == nil || group.ID != groupID || !supportsGroupModelMapping(group.Platform) {
		return mapping
	}
	if target, matched := resolveRequestedModelInMapping(group.ModelMapping, strings.TrimSpace(model)); matched && target != "" {
		mapping.MappedModel = target
		mapping.Mapped = true
		mapping.GroupMapped = true
	}
	return mapping
}

// Group mapping is applied before account selection. Restriction checks
// must resolve from the client model retained by the handler, not map its target
// a second time or treat that target as the originally requested billing model.
func groupModelMappingRequestedModel(ctx context.Context, groupID int64, model string) string {
	group, _ := ctx.Value(ctxkey.Group).(*Group)
	requestedModel, _ := ctx.Value(ctxkey.Model).(string)
	if group == nil || group.ID != groupID || !supportsGroupModelMapping(group.Platform) || requestedModel == "" {
		return model
	}
	if target, matched := resolveRequestedModelInMapping(group.ModelMapping, requestedModel); matched && target == model {
		return requestedModel
	}
	return model
}

func groupModelMappingMatched(ctx context.Context, requestedModel string) bool {
	if ctx == nil {
		return false
	}
	group, _ := ctx.Value(ctxkey.Group).(*Group)
	if group == nil || !supportsGroupModelMapping(group.Platform) {
		return false
	}
	target, matched := resolveRequestedModelInMapping(group.ModelMapping, strings.TrimSpace(requestedModel))
	return matched && target != ""
}

// Keep the original model in context so nested selection and forwarding do not
// chain group rules or restore account mapping for the group's target model.
func withGroupModelMappingForSelection(ctx context.Context, groupID *int64, model string) (context.Context, string) {
	if groupID == nil {
		return ctx, model
	}
	original := groupModelMappingRequestedModel(ctx, *groupID, model)
	mapping := applyGroupModelMapping(ctx, *groupID, original, ChannelMappingResult{MappedModel: model})
	if !mapping.GroupMapped {
		return ctx, model
	}
	return context.WithValue(ctx, ctxkey.Model, original), mapping.MappedModel
}

// ResolveRequestModelMapping keeps forwarding and selection on the same group
// rules when a non-Claude-Code request is routed to a fallback group.
func (s *GatewayService) ResolveRequestModelMapping(ctx context.Context, groupID *int64, model string) (context.Context, ChannelMappingResult) {
	mapping, _ := s.ResolveChannelMappingAndRestrict(ctx, groupID, model)
	group, _ := ctx.Value(ctxkey.Group).(*Group)
	if groupID != nil && group != nil && group.ID == *groupID && group.ClaudeCodeOnly && group.FallbackGroupID != nil {
		resolved, resolvedID, err := s.checkClaudeCodeRestriction(ctx, groupID)
		if err == nil && resolved != nil && resolved.ID != group.ID && (len(group.ModelMapping) > 0 || len(resolved.ModelMapping) > 0) {
			ctx = s.withGroupContext(ctx, resolved)
			mapping, _ = s.ResolveChannelMappingAndRestrict(ctx, resolvedID, model)
		}
	}
	return ctx, mapping
}

// AccountForGroupRequestedModel keeps shared snapshots and credentials unchanged. The view
// suppresses remapping while retaining the account's model support restrictions.
func AccountForGroupRequestedModel(ctx context.Context, account *Account, requestedModel string) *Account {
	if account == nil {
		return nil
	}
	final := groupModelMappingMatched(ctx, requestedModel)
	if account.groupModelMappingFinal == final {
		return account
	}
	view := *account
	view.groupModelMappingFinal = final
	return &view
}

func accountForGroupModelMapping(ctx context.Context, account *Account) *Account {
	if ctx == nil {
		return account
	}
	requestedModel, _ := ctx.Value(ctxkey.Model).(string)
	return AccountForGroupRequestedModel(ctx, account, requestedModel)
}

func accountsForGroupModelMapping(ctx context.Context, accounts []Account) []Account {
	requestedModel, _ := ctx.Value(ctxkey.Model).(string)
	if !groupModelMappingMatched(ctx, requestedModel) {
		return accounts
	}
	views := append([]Account(nil), accounts...)
	for i := range views {
		views[i].groupModelMappingFinal = true
	}
	return views
}
