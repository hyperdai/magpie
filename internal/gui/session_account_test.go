package gui

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"github.com/yetone/magpie/internal/gateway"
	"github.com/yetone/magpie/internal/provider"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 管理接口不接受凭空构造的会话身份，也不改变客户端登录。
func TestSessionAccountRequiresRecentCodexRoute(t *testing.T) {
	sandboxHome(t)
	old := served.Swap(nil)
	t.Cleanup(func() { served.Store(old) })
	mux := http.NewServeMux()
	sessionAccountRoutes(mux)
	for _, c := range []struct {
		method, target, body string
		status               int
	}{
		{"GET", "/api/gateway/session-account?id=1", "", 404},
		{"POST", "/api/gateway/session-account", `{"id":1,"account":"spare@example.com"}`, 404},
		{"POST", "/api/gateway/session-account", `{"agent":"codex","session":"invented","account":"spare@example.com"}`, 404},
		{"POST", "/api/gateway/session-account", `{`, 400},
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(c.method, c.target, strings.NewReader(c.body)))
		if rec.Code != c.status {
			t.Fatalf("%s %s: %d %s", c.method, c.target, rec.Code, rec.Body.String())
		}
	}
}

// 从真实网关记录解析会话后，管理接口将选择保存到同一会话。
func TestSessionAccountManagementRoundTrip(t *testing.T) {
	sandboxHome(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	claims := func(v map[string]any) string {
		b, _ := json.Marshal(v)
		return "h." + base64.RawURLEncoding.EncodeToString(b) + ".s"
	}
	token := claims(map[string]any{"exp": time.Now().Add(time.Hour).Unix()})
	os.MkdirAll(filepath.Join(home, ".codex"), 0o700)
	auth, _ := json.Marshal(map[string]any{"auth_mode": "chatgpt", "tokens": map[string]any{"id_token": claims(map[string]any{"email": "me@example.com"}), "access_token": token, "refresh_token": "fixture-refresh", "account_id": "fixture-account"}})
	os.WriteFile(filepath.Join(home, ".codex/auth.json"), auth, 0o600)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"r1\",\"model\":\"gpt-5.5\"}}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"usage\":{\"input_tokens\":7,\"output_tokens\":1}}}\n\n")
	}))
	defer backend.Close()
	oldBase := provider.CodexBase
	provider.CodexBase = backend.URL + "/backend-api/codex"
	defer func() { provider.CodexBase = oldBase }()
	gw := gateway.New()
	old := served.Swap(gw)
	defer served.Store(old)
	req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"codex/gpt-5.5","stream":true,"input":"ping"}`))
	req.Header.Set("User-Agent", "codex/0.166.0")
	req.Header.Set("session_id", "test-session")
	rec := httptest.NewRecorder()
	gw.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("gateway: %d %s", rec.Code, rec.Body.String())
	}
	rows := gw.Trace(t.Context(), 0, 0).Routes
	if len(rows) == 0 {
		t.Fatal("no route")
	}
	id := rows[len(rows)-1].ID
	mux := http.NewServeMux()
	sessionAccountRoutes(mux)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/gateway/session-account?id="+strconv.FormatInt(id, 10), nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "me@example.com") {
		t.Fatalf("choices: %d %s", rec.Code, rec.Body.String())
	}
	body, _ := json.Marshal(map[string]any{"id": id, "account": "me@example.com"})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/gateway/session-account", bytes.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	choice, err := gateway.SessionAccount("test-session")
	if err != nil || choice.Account != "me@example.com" {
		t.Fatalf("saved choice: %+v %v", choice, err)
	}
}
