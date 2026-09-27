package im

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestExtendedSlashParts(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		parts []string
		cmd   string
		ok    bool
	}{
		{"empty text", "", nil, "", false},
		{"whitespace only", "   ", nil, "", false},
		{"no slash prefix", "restart debug", nil, "", false},
		{"plain command", "/restart", []string{"/restart"}, "/restart", true},
		{"case folded cmd", "/RESTART DEBUG", []string{"/RESTART", "DEBUG"}, "/restart", true},
		{"trimmed", "  /config  ", []string{"/config"}, "/config", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parts, cmd, ok := extendedSlashParts(tc.text)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if cmd != tc.cmd {
				t.Fatalf("cmd = %q, want %q", cmd, tc.cmd)
			}
			if !reflect.DeepEqual(parts, tc.parts) {
				t.Fatalf("parts = %v, want %v", parts, tc.parts)
			}
		})
	}
}

func TestInvokeExtendedSlashCallback(t *testing.T) {
	t.Run("error wrapped with ❌", func(t *testing.T) {
		got := invokeExtendedSlashCallback("ignored", errors.New("boom"))
		if !got.Handled {
			t.Fatal("expected Handled=true")
		}
		if got.Response != "❌ boom" {
			t.Fatalf("Response = %q, want %q", got.Response, "❌ boom")
		}
	})
	t.Run("success passthrough", func(t *testing.T) {
		got := invokeExtendedSlashCallback("done: ok", nil)
		if !got.Handled {
			t.Fatal("expected Handled=true")
		}
		if got.Response != "done: ok" {
			t.Fatalf("Response = %q, want %q", got.Response, "done: ok")
		}
	})
}

func TestHandleRestartSlash(t *testing.T) {
	t.Run("nil callback unavailable message", func(t *testing.T) {
		got := handleRestartSlash([]string{"/restart"}, ExtendedIMSlashOptions{})
		if !got.Handled || got.Response != "❌ Restart not available in this mode." {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("debug arg case-insensitive", func(t *testing.T) {
		var gotDebug bool
		got := handleRestartSlash([]string{"/restart", "DEBUG"}, ExtendedIMSlashOptions{
			OnRestart: func(debug bool) (string, error) {
				gotDebug = debug
				return "restarting", nil
			},
		})
		if !gotDebug {
			t.Fatal("expected debug=true")
		}
		if !got.Handled || got.Response != "restarting" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("no arg not debug", func(t *testing.T) {
		var gotDebug = true
		_ = handleRestartSlash([]string{"/restart"}, ExtendedIMSlashOptions{
			OnRestart: func(debug bool) (string, error) {
				gotDebug = debug
				return "restarting", nil
			},
		})
		if gotDebug {
			t.Fatal("expected debug=false without arg")
		}
	})
	t.Run("error wrapped", func(t *testing.T) {
		got := handleRestartSlash([]string{"/restart"}, ExtendedIMSlashOptions{
			OnRestart: func(bool) (string, error) { return "", errors.New("busy") },
		})
		if got.Response != "❌ busy" {
			t.Fatalf("Response = %q, want %q", got.Response, "❌ busy")
		}
	})
}

func TestHandleProviderSlash(t *testing.T) {
	t.Run("nil callback unavailable message", func(t *testing.T) {
		got := handleProviderSlash([]string{"/provider"}, ExtendedIMSlashOptions{})
		if !got.Handled || got.Response != "❌ Provider switching not available in this mode." {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("arg parse", func(t *testing.T) {
		cases := []struct {
			parts        []string
			wantVendor   string
			wantEndpoint string
		}{
			{[]string{"/provider"}, "", ""},
			{[]string{"/provider", "V1"}, "V1", ""},
			{[]string{"/provider", "V1", "ep-2"}, "V1", "ep-2"},
		}
		for _, tc := range cases {
			var gotVendor, gotEndpoint string
			handleProviderSlash(tc.parts, ExtendedIMSlashOptions{
				OnProvider: func(vendor, endpoint string) (string, error) {
					gotVendor, gotEndpoint = vendor, endpoint
					return "switched", nil
				},
			})
			if gotVendor != tc.wantVendor || gotEndpoint != tc.wantEndpoint {
				t.Fatalf("parts %v: got (%q, %q), want (%q, %q)",
					tc.parts, gotVendor, gotEndpoint, tc.wantVendor, tc.wantEndpoint)
			}
		}
	})
	t.Run("error wrapped", func(t *testing.T) {
		got := handleProviderSlash([]string{"/provider", "x"}, ExtendedIMSlashOptions{
			OnProvider: func(string, string) (string, error) { return "", errors.New("nope") },
		})
		if got.Response != "❌ nope" {
			t.Fatalf("Response = %q", got.Response)
		}
	})
}

func TestHandleModelSlash(t *testing.T) {
	t.Run("nil callback unavailable message", func(t *testing.T) {
		got := handleModelSlash([]string{"/model"}, ExtendedIMSlashOptions{})
		if !got.Handled || got.Response != "❌ Model switching not available in this mode." {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("arg parse", func(t *testing.T) {
		var gotModel string
		handleModelSlash([]string{"/model", "glm-5"}, ExtendedIMSlashOptions{
			OnModel: func(model string) (string, error) {
				gotModel = model
				return "model set", nil
			},
		})
		if gotModel != "glm-5" {
			t.Fatalf("model = %q, want %q", gotModel, "glm-5")
		}
	})
	t.Run("error wrapped", func(t *testing.T) {
		got := handleModelSlash([]string{"/model", "x"}, ExtendedIMSlashOptions{
			OnModel: func(string) (string, error) { return "", errors.New("bad model") },
		})
		if got.Response != "❌ bad model" {
			t.Fatalf("Response = %q", got.Response)
		}
	})
}

func TestHandleConfigSlash(t *testing.T) {
	t.Run("nil callback unavailable message", func(t *testing.T) {
		got := handleConfigSlash(ExtendedIMSlashOptions{})
		if !got.Handled || got.Response != "❌ Config display not available in this mode." {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("success passthrough", func(t *testing.T) {
		got := handleConfigSlash(ExtendedIMSlashOptions{
			OnConfig: func() (string, error) { return "cfg: v1", nil },
		})
		if !got.Handled || got.Response != "cfg: v1" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("error wrapped", func(t *testing.T) {
		got := handleConfigSlash(ExtendedIMSlashOptions{
			OnConfig: func() (string, error) { return "", errors.New("cfg err") },
		})
		if got.Response != "❌ cfg err" {
			t.Fatalf("Response = %q", got.Response)
		}
	})
}

func TestExecuteExtendedIMSlashCommandRouting(t *testing.T) {
	t.Run("non-slash text unhandled", func(t *testing.T) {
		got := ExecuteExtendedIMSlashCommand(ExtendedIMSlashOptions{Text: "hello world"})
		if got.Handled {
			t.Fatalf("expected unhandled, got %+v", got)
		}
	})
	t.Run("common command delegation /help", func(t *testing.T) {
		got := ExecuteExtendedIMSlashCommand(ExtendedIMSlashOptions{
			Text:           "/help",
			HelpExtraLines: []string{"/custom - extra"},
		})
		if !got.Handled {
			t.Fatal("expected handled via common commands")
		}
		if !strings.Contains(got.Response, "Available commands:") || !strings.Contains(got.Response, "/custom - extra") {
			t.Fatalf("unexpected help response: %q", got.Response)
		}
	})
	t.Run("unknown command message with lowercased cmd", func(t *testing.T) {
		got := ExecuteExtendedIMSlashCommand(ExtendedIMSlashOptions{Text: "/NOPE arg"})
		if !got.Handled {
			t.Fatal("expected handled")
		}
		want := "Unknown command: /nope. Try /help"
		if got.Response != want {
			t.Fatalf("Response = %q, want %q", got.Response, want)
		}
	})
	t.Run("OnExtra intercepts with original-case parts", func(t *testing.T) {
		var gotParts []string
		got := ExecuteExtendedIMSlashCommand(ExtendedIMSlashOptions{
			Text: "/Custom extra",
			OnExtra: func(parts []string) (string, bool) {
				gotParts = parts
				return "extra-resp", true
			},
		})
		if !got.Handled || got.Response != "extra-resp" {
			t.Fatalf("got %+v", got)
		}
		if !reflect.DeepEqual(gotParts, []string{"/Custom", "extra"}) {
			t.Fatalf("parts = %v, want original case preserved", gotParts)
		}
	})
	t.Run("OnExtra decline falls through to unknown", func(t *testing.T) {
		got := ExecuteExtendedIMSlashCommand(ExtendedIMSlashOptions{
			Text:    "/custom",
			OnExtra: func([]string) (string, bool) { return "", false },
		})
		want := "Unknown command: /custom. Try /help"
		if !got.Handled || got.Response != want {
			t.Fatalf("got %+v, want %q", got, want)
		}
	})
	t.Run("restart routes with nil manager", func(t *testing.T) {
		got := ExecuteExtendedIMSlashCommand(ExtendedIMSlashOptions{
			Text: "/restart debug",
			OnRestart: func(debug bool) (string, error) {
				if !debug {
					t.Fatal("expected debug=true")
				}
				return "restarted", nil
			},
		})
		if !got.Handled || got.Response != "restarted" {
			t.Fatalf("got %+v", got)
		}
	})
}
