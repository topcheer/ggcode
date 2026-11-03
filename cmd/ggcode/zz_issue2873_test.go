package main

import "testing"

func TestValidatePluginInstallShapeTypeEnum(t *testing.T) {
	cases := []struct {
		name       string
		pluginType string
		wantErr    bool
		wantType   string
	}{
		{"grpc default", "grpc", false, "grpc"},
		{"command", "command", false, "command"},
		{"case variant Command", "Command", false, "command"},
		{"case variant GRPC with space", "  GRPC ", false, "grpc"},
		{"empty falls back to grpc", "", false, "grpc"},
		{"cmd is not accepted", "cmd", true, ""},
		{"commands is not accepted", "commands", true, ""},
		{"typo", "grpcc", true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := validatePluginInstallShape(tc.pluginType, []string{"my-tools"})
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for --type %q, got nil (type=%q)", tc.pluginType, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for --type %q: %v", tc.pluginType, err)
			}
			if got != tc.wantType {
				t.Fatalf("normalized type = %q, want %q", got, tc.wantType)
			}
		})
	}
}

func TestValidatePluginInstallShapeCommandPositionals(t *testing.T) {

	_, err := validatePluginInstallShape("command", []string{"my-tools", "./deploy.sh"})
	if err == nil {
		t.Fatal("expected error for --type command with extra positional, got nil")
	}
	assertContains(t, err.Error(), "plugins[].commands")

	got, err := validatePluginInstallShape("command", []string{"my-tools"})
	if err != nil {
		t.Fatalf("unexpected error for name-only command install: %v", err)
	}
	if got != "command" {
		t.Fatalf("normalized type = %q, want command", got)
	}

	got, err = validatePluginInstallShape("grpc", []string{"jira-tools", "python", "-m", "x"})
	if err != nil {
		t.Fatalf("unexpected error for grpc with command positionals: %v", err)
	}
	if got != "grpc" {
		t.Fatalf("normalized type = %q, want grpc", got)
	}
}

func TestParsePluginInstallArgsTypeVariants(t *testing.T) {
	pos, _, pt, err := parsePluginInstallArgs([]string{"my-tools", "--type=Command"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pt != "Command" {
		t.Fatalf("pluginType = %q, want raw \"Command\" (normalization is the validator's job)", pt)
	}
	if len(pos) != 1 || pos[0] != "my-tools" {
		t.Fatalf("positionals = %v, want [my-tools]", pos)
	}

	pos, _, pt, err = parsePluginInstallArgs([]string{"my-tools", "--type", "command", "--", "./deploy.sh"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pt != "command" {
		t.Fatalf("pluginType = %q, want command", pt)
	}
	if len(pos) != 2 || pos[1] != "./deploy.sh" {
		t.Fatalf("positionals = %v, want [my-tools ./deploy.sh] (validator must reject, not the parser)", pos)
	}
}

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return
		}
	}
	t.Fatalf("string %q does not contain %q", s, substr)
}
