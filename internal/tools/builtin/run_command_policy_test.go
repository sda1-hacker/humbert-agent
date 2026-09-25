package builtin

import "testing"

func TestRejectDestructiveCommand(t *testing.T) {
	for _, tc := range []struct {
		command string
		args    []string
		blocked bool
	}{
		{"rm", []string{"file"}, true},
		{"find", []string{".", "-exec", "rm", "{}", ";"}, true},
		{"find", []string{".", "-delete"}, true},
		{"git", []string{"clean", "-fd"}, true},
		{"find", []string{".", "-name", "*.go"}, false},
		{"pwd", nil, false},
	} {
		err := rejectDestructiveCommand(tc.command, tc.args)
		if (err != nil) != tc.blocked {
			t.Errorf("%s %v blocked=%v, err=%v", tc.command, tc.args, tc.blocked, err)
		}
	}
}
