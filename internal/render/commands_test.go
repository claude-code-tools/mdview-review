// internal/render/commands_test.go
package render

import "testing"

func TestBuiltinCommands(t *testing.T) {
	cmds := BuiltinCommands()
	if len(cmds) != 6 {
		t.Fatalf("want 6 builtin commands, got %d", len(cmds))
	}
	seen := map[string]bool{}
	for _, c := range cmds {
		if c.ID == "" || c.Label == "" {
			t.Fatalf("command with empty id/label: %+v", c)
		}
		if c.Prompt == "" {
			t.Fatalf("builtin command %q has empty prompt", c.ID)
		}
		if seen[c.ID] {
			t.Fatalf("duplicate command id %q", c.ID)
		}
		seen[c.ID] = true
		if c.Recommended {
			t.Fatalf("builtin command %q must not be recommended", c.ID)
		}
	}
}
