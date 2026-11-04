package gateway

import (
	"github.com/yetone/magpie/internal/provider"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func sessionPost(t *testing.T, session, header string) (int, string) {
	t.Helper()
	req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"codex/gpt-5.5","stream":true,"input":"ping"}`))
	req.Header.Set("User-Agent", "codex/0.166.0")
	req.Header.Set("session_id", session)
	if header != "" {
		req.Header.Set(AccountHeader, header)
	}
	rec := httptest.NewRecorder()
	New().Handler().ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func TestSessionAccountChangesNextRequest(t *testing.T) {
	heads := twoAccounts(t)
	if err := SetSessionAccount("first", "me@example.com"); err != nil {
		t.Fatal(err)
	}
	if code, body := sessionPost(t, "first", ""); code != 200 || !strings.Contains(body, "acct-1") {
		t.Fatalf("first: %d %s", code, body)
	}
	if err := SetSessionAccount("first", "spare@example.com"); err != nil {
		t.Fatal(err)
	}
	if code, body := sessionPost(t, "first", ""); code != 200 || !strings.Contains(body, "acct-2") {
		t.Fatalf("changed: %d %s", code, body)
	}
	if code, body := sessionPost(t, "other", ""); code != 200 || !strings.Contains(body, "acct-1") {
		t.Fatalf("other session: %d %s", code, body)
	}
	if code, body := sessionPost(t, "first", "me@example.com"); code != 200 || !strings.Contains(body, "acct-1") {
		t.Fatalf("explicit header: %d %s", code, body)
	}
	for _, h := range *heads {
		if h.Get(AccountHeader) != "" {
			t.Fatal("selection header went to vendor")
		}
	}
	choice, err := SessionAccount("first")
	if err != nil || choice.Account != "spare@example.com" {
		t.Fatalf("saved: %+v %v", choice, err)
	}
	if err := SetSessionAccount("first", ""); err != nil {
		t.Fatal(err)
	}
	choice, err = SessionAccount("first")
	if err != nil || choice.Account != "" {
		t.Fatalf("automatic: %+v %v", choice, err)
	}
}

func TestSessionAccountKeepsCapsAndDoesNotFallback(t *testing.T) {
	heads := twoAccounts(t)
	capUsage(t, map[string]float64{"me@example.com": 0, "spare@example.com": 90})
	// 使用现有额度设置，手动选择不能绕过它。
	if err := provider.SetAccountCap("codex", "spare@example.com", 80); err != nil {
		t.Fatal(err)
	}
	if err := SetSessionAccount("capped", "spare@example.com"); err != nil {
		t.Fatal(err)
	}
	if code, body := sessionPost(t, "capped", ""); code != 429 || !strings.Contains(body, "80%") {
		t.Fatalf("cap: %d %s", code, body)
	}
	if len(*heads) != 0 {
		t.Fatal("capped selection sent a vendor request")
	}
}

func TestSessionAccountInvalidFileDoesNotBecomeAutomatic(t *testing.T) {
	heads := twoAccounts(t)
	if err := os.WriteFile(sessionAccountsPath(), []byte(`{"broken"`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _ := sessionPost(t, "first", ""); code != http.StatusServiceUnavailable {
		t.Fatalf("corrupt settings: %d", code)
	}
	if err := SetSessionAccount("first", ""); err == nil {
		t.Fatal("overwrote unreadable settings")
	}
	if len(*heads) != 0 {
		t.Fatal("corrupt settings reached vendor")
	}
}

func TestSessionAccountDoesNotChangeOtherProvidersOrAnonymousCalls(t *testing.T) {
	twoAccounts(t)
	if err := SetSessionAccount("first", "spare@example.com"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ agent, model, session string }{{"claude", "codex/gpt-5.5", "first"}, {"codex", "unknown/model", "first"}, {"codex", "codex/gpt-5.5", ""}} {
		r := httptest.NewRequest("POST", "/v1/responses", nil)
		r.Header.Set("session_id", c.session)
		p, _, _ := provider.Resolve(c.model)
		got, err := withSessionAccount(r, c.agent, p)
		if err != nil || got.Header.Get(AccountHeader) != "" {
			t.Fatalf("unrelated: %+v %v", c, err)
		}
	}
	if err := SetSessionAccount("first", "not-enabled@example.com"); err == nil {
		t.Fatal("accepted unknown account")
	}
	if err := SetSessionAccount("", "spare@example.com"); err == nil {
		t.Fatal("accepted missing identity")
	}
}

func TestSessionAccountDoesNotRewriteRequestAlreadyRouted(t *testing.T) {
	twoAccounts(t)
	p, _, _ := provider.Resolve("codex/gpt-5.5")
	r := httptest.NewRequest("POST", "/v1/responses", nil)
	r.Header.Set("session_id", "first")
	if err := SetSessionAccount("first", "me@example.com"); err != nil {
		t.Fatal(err)
	}
	first, err := withSessionAccount(r, "codex", p)
	if err != nil {
		t.Fatal(err)
	}
	if err := SetSessionAccount("first", "spare@example.com"); err != nil {
		t.Fatal(err)
	}
	next, err := withSessionAccount(r, "codex", p)
	if err != nil || first.Header.Get(AccountHeader) != "me@example.com" || next.Header.Get(AccountHeader) != "spare@example.com" || r.Header.Get(AccountHeader) != "" {
		t.Fatalf("first=%s next=%s original=%s err=%v", first.Header.Get(AccountHeader), next.Header.Get(AccountHeader), r.Header.Get(AccountHeader), err)
	}
}
