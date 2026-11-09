package service

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// IsAnthropicNormalAPIEnabled is opt-in and only applies to Claude API Key accounts.
func (a *Account) IsAnthropicNormalAPIEnabled() bool {
	if a == nil || a.Platform != PlatformAnthropic || a.Type != AccountTypeAPIKey {
		return false
	}
	enabled, _ := a.Extra["anthropic_normal_api"].(bool)
	return enabled
}

// applyAnthropicNormalAPI runs after header overrides, on the outbound copy only.
// Keep the caller's client identity/session and tool history intact. Removing these
// explicit markers does not guarantee that an upstream cannot infer the client.
func applyAnthropicNormalAPI(account *Account, req *http.Request, body []byte) []byte {
	if !account.IsAnthropicNormalAPIEnabled() {
		return body
	}
	var betas []string
	for name, values := range req.Header {
		lower := strings.ToLower(name)
		if lower == "anthropic-beta" {
			betas = append(betas, values...)
			delete(req.Header, name)
		} else if lower == "user-agent" || lower == "accept" || lower == "content-type" || lower == "anthropic-version" ||
			lower == "x-app" || lower == "x-client-request-id" || lower == "x-anthropic-billing-header" ||
			lower == "anthropic-dangerous-direct-browser-access" || lower == "accept-language" ||
			strings.HasPrefix(lower, "sec-fetch-") || strings.HasPrefix(lower, "x-claude-code-") || strings.HasPrefix(lower, "x-stainless-") {
			delete(req.Header, name)
		}
	}
	// Hermes' native API-key path uses the Python SDK defaults, not its
	// HermesAgent attribution headers (those are for other provider routes).
	// Source: NousResearch/hermes-agent@c1db5a4fe1b8d515dd96470bf71b6db1a8e0eb5d,
	// agent/anthropic_adapter.py and pyproject.toml (anthropic==0.125.0).
	// Use a representative Linux/x64 CPython profile; leave transport headers
	// and feature betas to the existing transport and beta policy.
	for name, value := range map[string]string{
		"User-Agent":                  "Anthropic/Python 0.125.0",
		"Accept":                      "application/json",
		"Content-Type":                "application/json",
		"anthropic-version":           "2023-06-01",
		"X-Stainless-Lang":            "python",
		"X-Stainless-Package-Version": "0.125.0",
		"X-Stainless-OS":              "Linux",
		"X-Stainless-Arch":            "x64",
		"X-Stainless-Runtime":         "CPython",
		"X-Stainless-Runtime-Version": "3.14.8",
		"X-Stainless-Async":           "false",
		"x-stainless-retry-count":     "0",
		"x-stainless-read-timeout":    "900.0",
		"X-Stainless-Timeout":         "NOT_GIVEN",
	} {
		setHeaderRaw(req.Header, name, value)
	}
	if gjson.GetBytes(body, "stream").Bool() && !strings.HasSuffix(strings.TrimRight(req.URL.Path, "/"), "/count_tokens") {
		setHeaderRaw(req.Header, "x-stainless-helper-method", "stream")
		setHeaderRaw(req.Header, "x-stainless-stream-helper", "messages")
	}
	beta := stripBetaTokensWithSet(strings.Join(betas, ","), map[string]struct{}{
		claude.BetaClaudeCode: {}, claude.BetaOAuth: {},
	})
	if beta != "" {
		setHeaderRaw(req.Header, "anthropic-beta", beta)
	}

	out := body
	if uid := gjson.GetBytes(out, "metadata.user_id").String(); ParseMetadataUserID(uid) != nil {
		out, _ = sjson.DeleteBytes(out, "metadata.user_id")
	}
	system := gjson.GetBytes(out, "system")
	if system.Type == gjson.String {
		if text := normalAPISystemText(system.String()); text != system.String() {
			if text == "" {
				out, _ = sjson.DeleteBytes(out, "system")
			} else {
				out, _ = sjson.SetBytes(out, "system", text)
			}
		}
	} else if system.IsArray() {
		blocks := make([][]byte, 0)
		changed := false
		system.ForEach(func(_, block gjson.Result) bool {
			raw := []byte(block.Raw)
			if block.Get("type").String() == "text" {
				original := block.Get("text").String()
				if text := normalAPISystemText(original); text != original {
					changed = true
					if text == "" {
						return true
					}
					raw, _ = sjson.SetBytes(raw, "text", text)
				}
			}
			blocks = append(blocks, raw)
			return true
		})
		if changed {
			if len(blocks) == 0 {
				out, _ = sjson.DeleteBytes(out, "system")
			} else {
				out, _ = sjson.SetRawBytes(out, "system", buildJSONArrayRaw(blocks))
			}
		}
	}
	if !bytes.Equal(out, body) {
		// Keep the wire body, retry reader and Content-Length consistent.
		req.Body = io.NopCloser(bytes.NewReader(out))
		req.ContentLength = int64(len(out))
		req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(out)), nil }
	}
	return out
}

// Only leading attribution and identity sentences in a system block are replaced;
// project instructions, quoted client names and historical messages are not scrubbed.
func normalAPISystemText(text string) string {
	trimmed := strings.TrimLeft(text, " \t\r\n")
	line, rest, _ := strings.Cut(trimmed, "\n")
	if strings.HasPrefix(line, claudeCodeBillingHeaderPrefix+":") &&
		strings.Contains(line, "cc_version=") &&
		(strings.Contains(line, claudeCodeEntrypointMarker) || strings.Contains(line, "cch=")) {
		text = rest
		if strings.TrimSpace(text) == "" {
			return ""
		}
		trimmed = strings.TrimLeft(text, " \t\r\n")
	}
	identities := [][2]string{
		{"You are Claude Code, Anthropic's official CLI for Claude, running within the Claude Agent SDK.", "You are a coding assistant."},
		{claudeCodeSystemPrompt, "You are a coding assistant."},
		{"You are a Claude agent, built on Anthropic's Claude Agent SDK.", "You are a coding assistant."},
		{"You are a file search specialist for Claude Code, Anthropic's official CLI for Claude.", "You are a file search specialist."},
	}
	for _, identity := range identities {
		if strings.HasPrefix(trimmed, identity[0]) {
			return text[:len(text)-len(trimmed)] + identity[1] + strings.TrimPrefix(trimmed, identity[0])
		}
	}
	return text
}
