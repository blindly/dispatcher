package main

import "testing"

func TestBuildCrontab_AddsToEmpty(t *testing.T) {
	newLine := "*/5 * * * * cd /proj && /bin/dispatch >> .dispatcher/logs/dispatcher.log 2>&1"
	content, status := buildCrontab("", newLine, "/proj")
	if status != cronAdded {
		t.Errorf("status = %v, want cronAdded", status)
	}
	if content != newLine+"\n" {
		t.Errorf("content = %q", content)
	}
}

func TestBuildCrontab_AppendsAlongsideUnrelated(t *testing.T) {
	existing := "0 0 * * * /usr/bin/backup\n"
	newLine := "*/5 * * * * cd /proj && /bin/dispatch >> .dispatcher/logs/dispatcher.log 2>&1"
	content, status := buildCrontab(existing, newLine, "/proj")
	if status != cronAdded {
		t.Errorf("status = %v, want cronAdded", status)
	}
	want := "0 0 * * * /usr/bin/backup\n" + newLine + "\n"
	if content != want {
		t.Errorf("content = %q, want %q", content, want)
	}
}

func TestBuildCrontab_UnchangedWhenIdentical(t *testing.T) {
	line := "*/5 * * * * cd /proj && /bin/dispatch >> .dispatcher/logs/dispatcher.log 2>&1"
	existing := line + "\n"
	content, status := buildCrontab(existing, line, "/proj")
	if status != cronUnchanged {
		t.Errorf("status = %v, want cronUnchanged", status)
	}
	if content != existing {
		t.Errorf("content = %q", content)
	}
}

func TestBuildCrontab_UpdatesWhenScheduleDiffers(t *testing.T) {
	oldLine := "*/5 * * * * cd /proj && /bin/dispatch >> .dispatcher/logs/dispatcher.log 2>&1"
	newLine := "*/10 * * * * cd /proj && /bin/dispatch >> .dispatcher/logs/dispatcher.log 2>&1"
	existing := "0 0 * * * /usr/bin/backup\n" + oldLine + "\n"
	content, status := buildCrontab(existing, newLine, "/proj")
	if status != cronUpdated {
		t.Errorf("status = %v, want cronUpdated", status)
	}
	want := "0 0 * * * /usr/bin/backup\n" + newLine + "\n"
	if content != want {
		t.Errorf("content = %q, want %q", content, want)
	}
}

func TestBuildCrontab_OnlyMatchesThisProject(t *testing.T) {
	otherLine := "*/5 * * * * cd /other && /bin/dispatch >> .dispatcher/logs/dispatcher.log 2>&1"
	newLine := "*/5 * * * * cd /proj && /bin/dispatch >> .dispatcher/logs/dispatcher.log 2>&1"
	existing := otherLine + "\n"
	content, status := buildCrontab(existing, newLine, "/proj")
	if status != cronAdded {
		t.Errorf("status = %v, want cronAdded (other project's line must not match)", status)
	}
	want := otherLine + "\n" + newLine + "\n"
	if content != want {
		t.Errorf("content = %q, want %q", content, want)
	}
}

func TestBuildCrontab_DedupesMultipleMatches(t *testing.T) {
	newLine := "*/5 * * * * cd /proj && /bin/dispatch >> .dispatcher/logs/dispatcher.log 2>&1"
	oldLine := "*/2 * * * * cd /proj && /bin/dispatch >> .dispatcher/logs/dispatcher.log 2>&1"
	existing := oldLine + "\n0 0 * * * /usr/bin/backup\n" + oldLine + "\n"
	content, status := buildCrontab(existing, newLine, "/proj")
	if status != cronUpdated {
		t.Errorf("status = %v, want cronUpdated", status)
	}
	want := newLine + "\n0 0 * * * /usr/bin/backup\n"
	if content != want {
		t.Errorf("content = %q, want %q", content, want)
	}
}

func TestBuildCrontab_DropsDuplicateEvenWhenMatchIdentical(t *testing.T) {
	line := "*/5 * * * * cd /proj && /bin/dispatch >> .dispatcher/logs/dispatcher.log 2>&1"
	existing := line + "\n" + line + "\n"
	content, status := buildCrontab(existing, line, "/proj")
	if status != cronUpdated {
		t.Errorf("status = %v, want cronUpdated (duplicate was dropped)", status)
	}
	want := line + "\n"
	if content != want {
		t.Errorf("content = %q, want %q", content, want)
	}
}
