package display

import "testing"

func TestFormatInterval_Minutes(t *testing.T) {
	if got := FormatInterval(300); got != "5m" {
		t.Errorf("got %q, want 5m", got)
	}
}

func TestFormatInterval_Hours(t *testing.T) {
	if got := FormatInterval(7200); got != "2h" {
		t.Errorf("got %q, want 2h", got)
	}
}

func TestFormatInterval_Days(t *testing.T) {
	if got := FormatInterval(86400); got != "1d" {
		t.Errorf("got %q, want 1d", got)
	}
}

func TestFormatInterval_Weeks(t *testing.T) {
	if got := FormatInterval(604800); got != "1w" {
		t.Errorf("got %q, want 1w", got)
	}
}

func TestFormatDt_Valid(t *testing.T) {
	got := FormatDt("2025-01-15T10:30:00Z")
	if got != "2025-01-15 10:30" {
		t.Errorf("got %q", got)
	}
}

func TestFormatDt_Empty(t *testing.T) {
	if got := FormatDt(""); got != "-" {
		t.Errorf("got %q, want -", got)
	}
}
