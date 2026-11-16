package gateway

import (
	"net/http"

	"github.com/yetone/magpie/internal/settings"
)

// codexSuggestionsTo 只为主动建议及其安全检查选择路由，保留后续协议与权限处理。
func codexSuggestionsTo(header http.Header, body []byte) string {
	switch requestCallKind(header, requestSessionMetadata(header, body)) {
	case "ambient_suggestions", "ambient_suggestion_safety":
		return settings.Shared().CodexSuggestions
	}
	return ""
}

type codexSuggestionsKey struct{}

// codexSuggestionsFor 复用预算阶段的选择，其余请求读取当前配置。
func codexSuggestionsFor(request *http.Request, body []byte) string {
	if selected, ok := request.Context().Value(codexSuggestionsKey{}).(string); ok {
		return selected
	}
	return codexSuggestionsTo(request.Header, body)
}
