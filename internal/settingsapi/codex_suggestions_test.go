package settingsapi

import (
	"testing"

	"github.com/yetone/magpie/internal/settings"
)

func TestCodexSuggestionsSetting(t *testing.T) {
	home(t)
	for _, model := range []string{"fixture/model", "group/suggestions", ""} {
		body, err := Body(settings.Load(), "codexSuggestions", []byte(`"`+model+`"`))
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := Apply(body, false); err != nil {
			t.Fatal(err)
		}
		if got := settings.Load().CodexSuggestions; got != model {
			t.Fatalf("saved %q, want %q", got, model)
		}
	}
	if _, _, err := Apply([]byte(`{"codexSuggestions":"group/suggestions"}`), false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Apply([]byte(`{"theme":"dark"}`), false); err != nil {
		t.Fatal(err)
	}
	if got := settings.Load().CodexSuggestions; got != "group/suggestions" {
		t.Fatalf("another settings save lost %q", got)
	}
	for _, invalid := range []string{"off", "bare-model"} {
		if _, _, err := Apply([]byte(`{"codexSuggestions":"`+invalid+`"}`), false); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
		if got := settings.Load().CodexSuggestions; got != "group/suggestions" {
			t.Fatalf("refused save changed the model to %q", got)
		}
	}
}
