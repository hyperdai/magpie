package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/access"
	"github.com/yetone/magpie/internal/gwbudget"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/settings"
	"github.com/yetone/magpie/internal/usage"
)

const ambientSchema = `{"type":"object","properties":{"safe":{"type":"boolean"}},"required":["safe"],"additionalProperties":false}`

func TestCodexSuggestionsRouting(t *testing.T) {
	for _, path := range []string{CodexPath + "/responses", "/v1/responses"} {
		for _, kind := range []string{"ambient_suggestions", "ambient_suggestion_safety"} {
			t.Run(path+"/"+kind, func(t *testing.T) {
				fakeModel := &fake{t: t, reply: sse(`data: {"type":"response.completed","response":{"id":"fixture","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"{\"safe\":false}"}]}],"usage":{"input_tokens":3,"output_tokens":2}}}`)}
				upstream := setup(t, provider.Responses, fakeModel)
				if err := provider.Save(provider.Provider{ID: "original", Name: "Original", Key: "fixture-key", Responses: upstream.URL + "/v1", Models: []string{"m2"}}); err != nil {
					t.Fatal(err)
				}
				originalCalls := 0
				chatgpt(t, func(writer http.ResponseWriter, request *http.Request) {
					originalCalls++
					io.WriteString(writer, fakeModel.reply)
				})
				if err := provider.SaveGroup(provider.Group{ID: "suggestions", Members: []string{"fake/m1"}, Routing: provider.Ordered}); err != nil {
					t.Fatal(err)
				}
				server := New()
				request := func(source string) *httptest.ResponseRecorder {
					asked := "gpt-6-luna"
					if path == "/v1/responses" {
						asked = "original/m2"
					}
					body := `{"model":"` + asked + `","stream":true,"input":"Return the requested fixture result.","text":{"format":{"type":"json_schema","name":"fixture","strict":true,"schema":` + ambientSchema + `}}}`
					recorder := httptest.NewRecorder()
					req := httptest.NewRequest("POST", path, strings.NewReader(body))
					req.Header.Set("Authorization", "Bearer fixture-token")
					req.Header.Set("User-Agent", "codex-cli/fixture")
					req.Header.Set("x-codex-turn-metadata", `{"thread_source":"`+source+`"}`)
					server.Handler().ServeHTTP(recorder, req)
					return recorder
				}
				record := request(kind)
				if record.Code != http.StatusOK {
					t.Fatalf("default: %d %s", record.Code, record.Body.String())
				}
				if path == CodexPath+"/responses" && (originalCalls != 1 || fakeModel.calls != 0) {
					t.Fatalf("default changed: original %d, configured %d", originalCalls, fakeModel.calls)
				}
				config := settings.Load()
				config.CodexSuggestions = "group/suggestions"
				if err := settings.Save(config); err != nil {
					t.Fatal(err)
				}
				before := fakeModel.calls
				record = request(kind)
				text, _ := outputText(t, record.Body.String())
				if record.Code != http.StatusOK || fakeModel.calls != before+1 || text != `{"safe":false}` {
					t.Fatalf("routed: %d %s, calls %d", record.Code, record.Body.String(), fakeModel.calls)
				}
				var sent struct {
					Model string          `json:"model"`
					Text  json.RawMessage `json:"text"`
				}
				if err := json.Unmarshal(fakeModel.got, &sent); err != nil {
					t.Fatal(err)
				}
				if sent.Model != "m1" || !strings.Contains(string(sent.Text), `"strict":true`) || !strings.Contains(string(sent.Text), `"safe"`) {
					t.Fatalf("request schema changed: %s", fakeModel.got)
				}
				records := usage.Load(time.Time{})
				if last := records[len(records)-1]; last.Provider != "fake" || last.Kind != kind {
					t.Fatalf("purpose or provider changed: %+v", last)
				}
				if path == CodexPath+"/responses" {
					request("")
					if originalCalls != 2 {
						t.Fatal("the main conversation was rerouted")
					}
				}
			})
		}
	}
}
func TestCodexSuggestionsFallbackAndLimits(t *testing.T) {
	fakeModel := &fake{t: t, reply: sse(`data: {"type":"response.completed","response":{"id":"fixture","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"{\"safe\":false}"}]}]}}`)}
	setup(t, provider.Responses, fakeModel)
	originalCalls := 0
	chatgpt(t, func(writer http.ResponseWriter, request *http.Request) {
		originalCalls++
		io.WriteString(writer, fakeModel.reply)
	})
	var primaryCalls atomic.Int64
	primary := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		primaryCalls.Add(1)
		http.Error(writer, "fixture unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(primary.Close)
	if err := provider.Save(provider.Provider{ID: "primary", Name: "Primary", Key: "fixture-key", Responses: primary.URL + "/v1", Models: []string{"m1"}}); err != nil {
		t.Fatal(err)
	}
	if err := provider.SaveGroup(provider.Group{ID: "suggestions", Members: []string{"primary/m1", "fake/m1"}, Routing: provider.Ordered}); err != nil {
		t.Fatal(err)
	}
	config := settings.Load()
	config.CodexSuggestions = "group/suggestions"
	if err := settings.Save(config); err != nil {
		t.Fatal(err)
	}
	server := New()
	post := func(secret string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest("POST", CodexPath+"/responses", strings.NewReader(`{"model":"gpt-6-luna","stream":true,"input":"Return the fixture result."}`))
		request.Header.Set("Authorization", "Bearer "+secret)
		request.Header.Set("x-codex-turn-metadata", `{"thread_source":"ambient_suggestion_safety"}`)
		server.Handler().ServeHTTP(recorder, request)
		return recorder
	}
	if record := post("fixture-token"); record.Code != http.StatusOK || primaryCalls.Load() == 0 || fakeModel.calls != 1 || originalCalls != 0 {
		t.Fatalf("fallback: %d %s; primary %d, fallback %d, original %d", record.Code, record.Body.String(), primaryCalls.Load(), fakeModel.calls, originalCalls)
	}
	keys, secrets := newCaller(t, "Restricted")
	if _, err := access.Update("models-key", access.Change{Key: keys[0].ID, Models: []string{"other/model"}}); err != nil {
		t.Fatal(err)
	}
	if record := post(secrets[0]); record.Code != http.StatusForbidden || fakeModel.calls != 1 || originalCalls != 0 {
		t.Fatalf("restricted: %d %s; fallback %d, original %d", record.Code, record.Body.String(), fakeModel.calls, originalCalls)
	}
	config.CodexSuggestions = "missing/model"
	if err := settings.Save(config); err != nil {
		t.Fatal(err)
	}
	if record := post("fixture-token"); record.Code != http.StatusNotFound || fakeModel.calls != 1 || originalCalls != 0 {
		t.Fatalf("missing target: %d %s; fallback %d, original %d", record.Code, record.Body.String(), fakeModel.calls, originalCalls)
	}
}

type suggestionBudget struct {
	model     string
	onReserve func()
}

// Reserve 记录预算预留使用的模型，确保它与真正发送的模型一致。
func (observer *suggestionBudget) Reserve(_ access.Identity, _ int64, model string, _ time.Time) (func(), gwbudget.Refusal) {
	observer.model = model
	if observer.onReserve != nil {
		observer.onReserve()
	}
	return func() {}, nil
}

func TestCodexSuggestionsReservationUsesRoutedModel(t *testing.T) {
	for _, path := range []string{CodexPath + "/responses", "/v1/responses", "/responses"} {
		t.Run(path, func(t *testing.T) {
			fakeModel := &fake{t: t, reply: sse(`data: {"type":"response.completed","response":{"id":"fixture","output":[]}}`)}
			setup(t, provider.Responses, fakeModel)
			config := settings.Load()
			config.CodexSuggestions = "fake/m1"
			if err := settings.Save(config); err != nil {
				t.Fatal(err)
			}
			keys, secrets := newCaller(t, "Capped")
			setLimit(t, keys[0].ID, &access.Limit{Period: "day", Tokens: 100, Models: []string{"fake/*"}})
			observer := &suggestionBudget{onReserve: func() {
				config.CodexSuggestions = "missing/model"
				if err := settings.Save(config); err != nil {
					t.Fatal(err)
				}
			}}
			t.Cleanup(gwbudget.Limit(observer))
			request := httptest.NewRequest("POST", path, strings.NewReader(`{"model":"gpt-6-luna","stream":true,"input":"Return the fixture result."}`))
			request.Header.Set("Authorization", "Bearer "+secrets[0])
			request.Header.Set("x-codex-turn-metadata", `{"thread_source":"ambient_suggestions"}`)
			record := httptest.NewRecorder()
			New().Handler().ServeHTTP(record, request)
			if record.Code != http.StatusOK || observer.model != "fake/m1" || fakeModel.calls != 1 {
				t.Fatalf("reservation: model %q, calls %d, response %d %s", observer.model, fakeModel.calls, record.Code, record.Body.String())
			}
		})
	}
}
