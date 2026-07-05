//go:build !windows

package runner

import "testing"

func TestStripANSI(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain text", "hello world", "hello world"},
		{"CSI color", "\x1b[32mok\x1b[0m", "ok"},
		{"CSI clear line", "\x1b[2Kdone", "done"},
		{"CSI cursor move", "\x1b[1;1Htop", "top"},
		{"OSC title", "\x1b]0;window title\x07rest", "rest"},
		{"OSC title ST", "\x1b]0;title\x1b\\rest", "rest"},
		{"mixed", "\x1b[1mbold\x1b[0m \x1b[32mgreen\x1b[0m", "bold green"},
		{"bare ESC", "\x1b[Qtext", "text"},
		{"trailing ESC", "text\x1b", "text"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripANSI(tt.input)
			if got != tt.want {
				t.Errorf("stripANSI(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}