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
		} else if lower == "user-agent" || lower == "x-app" || lower == "x-anthropic-billing-header" || strings.HasPrefix(lower, "x-claude-code-") {
			delete(req.Header, name)
		}
	}
	req.Header.Set("User-Agent", "sub2api")
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
