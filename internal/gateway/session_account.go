package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/filelock"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/steady"
)

// SessionAccountChoice 返回当前会话的账号选择及仍在池中启用的账号。
type SessionAccountChoice struct {
	Account  string   `json:"account"`
	Accounts []string `json:"accounts"`
}

func sessionAccountsPath() string {
	return filepath.Join(filepath.Dir(provider.Path()), "session-accounts.json")
}

func sessionAccountKey(session string) string {
	sum := sha256.Sum256([]byte("codex\x00" + session))
	return hex.EncodeToString(sum[:])
}

func readSessionAccounts() (map[string]string, error) {
	b, err := steady.ReadFile(sessionAccountsPath())
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var saved map[string]string
	if err = json.Unmarshal(b, &saved); err != nil {
		return nil, err
	}
	if saved == nil {
		return nil, fmt.Errorf("invalid session account settings")
	}
	return saved, nil
}

func validAccountSession(session string) bool {
	return strings.TrimSpace(session) != "" && len(session) <= 128 && !strings.ContainsAny(session, "\r\n\x00")
}

// SessionAccount 读取选择；账号下线后仍保留选择，由请求路由明确报错。
func SessionAccount(session string) (SessionAccountChoice, error) {
	out := SessionAccountChoice{Accounts: []string{}}
	if !validAccountSession(session) {
		return out, fmt.Errorf("invalid session id")
	}
	saved, err := readSessionAccounts()
	if err != nil {
		return out, err
	}
	out.Account = saved[sessionAccountKey(session)]
	p, err := provider.Find("codex")
	if err != nil {
		return out, nil
	}
	for _, p := range append([]provider.Provider{*p}, p.AlsoOn()...) {
		if p.Account != nil && p.Account.Agent == "codex" && !p.OwnPaused() {
			out.Accounts = append(out.Accounts, p.Account.User)
		}
	}
	return out, nil
}

// SetSessionAccount 持久化单个 Codex 会话的选择；空值恢复自动路由。
func SetSessionAccount(session, account string) error {
	if !validAccountSession(session) {
		return fmt.Errorf("invalid session id")
	}
	if account != "" {
		choice, err := SessionAccount(session)
		if err != nil {
			return err
		}
		found := false
		for _, user := range choice.Accounts {
			if user == account {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("account is not enabled for Codex")
		}
	}
	unlock, held := filelock.Lock(sessionAccountsPath()+".lock", 3*time.Second)
	defer unlock()
	if !held {
		return fmt.Errorf("session account settings are busy")
	}
	saved, err := readSessionAccounts()
	if err != nil {
		return err
	}
	key := sessionAccountKey(session)
	if account == "" {
		delete(saved, key)
	} else {
		if _, exists := saved[key]; !exists && len(saved) >= 512 {
			return fmt.Errorf("too many session account selections; clear an old selection first")
		}
		saved[key] = account
	}
	b, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	return steady.WriteFile(sessionAccountsPath(), b, 0o600)
}

// withSessionAccount 在下一次请求路由前固定账号，不改客户端登录或已发出的请求。
func withSessionAccount(r *http.Request, agent string, p provider.Provider) (*http.Request, error) {
	if agent != "codex" || r.Header.Get(AccountHeader) != "" {
		return r, nil
	}
	if p.Account == nil || p.Account.Agent != "codex" {
		return r, nil
	}
	session := sessionOf(r.Header)
	if session == "" {
		return r, nil
	}
	saved, err := readSessionAccounts()
	if err != nil {
		return r, err
	}
	if account := saved[sessionAccountKey(session)]; account != "" {
		r = r.Clone(r.Context())
		r.Header.Set(AccountHeader, account)
	}
	return r, nil
}
