# Live Notifications Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `dispatch notify` subcommand that lets running jobs send real-time notifications through configured Discord/ntfy channels.

**Architecture:** New `notify` CLI subcommand loads config and calls a new `SendLiveNotification()` function in the notify package. The runner injects `DISPATCH_JOB` env var so notifications auto-tag with the job name. No DB, no lock, no new dependencies.

**Tech Stack:** Go standard library, existing notify package patterns

---

### Task 1: SendLiveNotification in notify package (tests)

**Files:**
- Modify: `internal/notify/notify_test.go`

- [ ] **Step 1: Write test for Discord live notification with job name**

Add to `internal/notify/notify_test.go`:

```go
func TestSendLiveDiscord_WithJob(t *testing.T) {
	var gotPayload map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotPayload)
		w.WriteHeader(204)
	}))
	defer server.Close()

	cfg := NotifyConfig{DiscordWebhook: server.URL}
	SendLiveNotification("Backup 50% complete", "db-backup", cfg)

	embeds := gotPayload["embeds"].([]interface{})
	embed := embeds[0].(map[string]interface{})

	title := embed["title"].(string)
	if title != "[db-backup] Live Update" {
		t.Errorf("title = %q, want [db-backup] Live Update", title)
	}

	color := int(embed["color"].(float64))
	if color != 0x7289DA {
		t.Errorf("color = %x, want 7289DA", color)
	}

	desc := embed["description"].(string)
	if desc != "Backup 50% complete" {
		t.Errorf("description = %q, want message text", desc)
	}
}
```

- [ ] **Step 2: Write test for Discord live notification without job name**

Add to `internal/notify/notify_test.go`:

```go
func TestSendLiveDiscord_NoJob(t *testing.T) {
	var gotPayload map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotPayload)
		w.WriteHeader(204)
	}))
	defer server.Close()

	cfg := NotifyConfig{DiscordWebhook: server.URL}
	SendLiveNotification("Something happened", "", cfg)

	embeds := gotPayload["embeds"].([]interface{})
	embed := embeds[0].(map[string]interface{})

	title := embed["title"].(string)
	if title != "Live Update" {
		t.Errorf("title = %q, want Live Update", title)
	}
}
```

- [ ] **Step 3: Write test for ntfy live notification**

Add to `internal/notify/notify_test.go`:

```go
func TestSendLiveNtfy_WithJob(t *testing.T) {
	var gotTitle, gotTags, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTitle = r.Header.Get("Title")
		gotTags = r.Header.Get("Tags")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(200)
	}))
	defer server.Close()

	cfg := NotifyConfig{NtfyURL: server.URL}
	SendLiveNotification("Step 3 done", "db-backup", cfg)

	if gotTitle != "[db-backup] Live Update" {
		t.Errorf("title = %q, want [db-backup] Live Update", gotTitle)
	}
	if gotTags != "speech_balloon" {
		t.Errorf("tags = %q, want speech_balloon", gotTags)
	}
	if gotBody != "Step 3 done" {
		t.Errorf("body = %q, want message text", gotBody)
	}
}
```

- [ ] **Step 4: Write test for no channels configured**

Add to `internal/notify/notify_test.go`:

```go
func TestSendLiveNotification_NoChannels(t *testing.T) {
	cfg := NotifyConfig{}
	err := SendLiveNotification("hello", "test", cfg)
	if err == nil {
		t.Error("expected error when no channels configured")
	}
}
```

- [ ] **Step 5: Run tests to verify they fail**

Run: `go test ./internal/notify/ -run TestSendLive -v`
Expected: compilation errors — `SendLiveNotification` doesn't exist yet.

- [ ] **Step 6: Commit test file**

```bash
git add internal/notify/notify_test.go
git commit -m "test: add tests for live notification feature"
```

---

### Task 2: SendLiveNotification implementation

**Files:**
- Modify: `internal/notify/notify.go`

- [ ] **Step 1: Add SendLiveNotification function**

Add to the end of `internal/notify/notify.go`:

```go
func SendLiveNotification(message string, jobName string, cfg NotifyConfig) error {
	title := "Live Update"
	if jobName != "" {
		title = fmt.Sprintf("[%s] Live Update", jobName)
	}

	sent := false

	// Discord
	if cfg.DiscordWebhook != "" {
		payload := map[string]interface{}{
			"embeds": []map[string]interface{}{
				{
					"title":       title,
					"color":       0x7289DA,
					"description": message,
					"timestamp":   time.Now().UTC().Format(time.RFC3339),
				},
			},
		}
		body, err := json.Marshal(payload)
		if err == nil {
			client := &http.Client{Timeout: 10 * time.Second}
			resp, err := client.Post(cfg.DiscordWebhook, "application/json", bytes.NewReader(body))
			if err != nil {
				fmt.Fprintf(os.Stderr, "Discord live notification failed: %v\n", err)
			} else {
				resp.Body.Close()
				sent = true
			}
		}
	}

	// ntfy
	ntfyURL := cfg.NtfyURL
	if ntfyURL == "" && cfg.NtfyTopic != "" {
		ntfyURL = "https://ntfy.sh"
	}
	if ntfyURL != "" {
		if cfg.NtfyTopic != "" {
			ntfyURL = strings.TrimRight(ntfyURL, "/") + "/" + cfg.NtfyTopic
		}
		req, err := http.NewRequest("POST", ntfyURL, strings.NewReader(message))
		if err == nil {
			req.Header.Set("Title", title)
			req.Header.Set("Priority", "default")
			req.Header.Set("Tags", "speech_balloon")
			if cfg.NtfyToken != "" {
				req.Header.Set("Authorization", "Bearer "+cfg.NtfyToken)
			}
			client := &http.Client{Timeout: 10 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				fmt.Fprintf(os.Stderr, "ntfy live notification failed: %v\n", err)
			} else {
				resp.Body.Close()
				sent = true
			}
		}
	}

	if !sent {
		return fmt.Errorf("no notification channels configured")
	}
	return nil
}
```

Note: This requires adding `"os"` to the imports at the top of `notify.go`.

- [ ] **Step 2: Run tests to verify they pass**

Run: `go test ./internal/notify/ -run TestSendLive -v`
Expected: all 4 tests pass.

- [ ] **Step 3: Run full notify test suite**

Run: `go test ./internal/notify/ -v`
Expected: all tests pass (existing + new).

- [ ] **Step 4: Commit**

```bash
git add internal/notify/notify.go
git commit -m "feat: add SendLiveNotification for real-time job notifications"
```

---

### Task 3: `notify` subcommand in main.go

**Files:**
- Modify: `main.go`

- [ ] **Step 1: Add `notify` to the usage string**

In `main.go`, find the usage constant (line 24). Add the `notify` command between `watch` and `enable`:

```
  notify       Send a live notification (for use inside jobs)
```

- [ ] **Step 2: Add the notify subcommand handler**

In `main.go`, add the handler after the `validate` block (after line 301) and before the `dispDir := ensureDispatcherDir(configDir)` line (line 303). The `notify` command needs config (for webhook URLs) but not the DB or dispatcher directory:

```go
	if cmd == "notify" {
		if len(args) == 0 {
			fmt.Fprintln(os.Stderr, "usage: dispatch notify [--job NAME] <message...>")
			os.Exit(1)
		}
		jobName := os.Getenv("DISPATCH_JOB")
		var msgParts []string
		for i := 0; i < len(args); i++ {
			if args[i] == "--job" && i+1 < len(args) {
				jobName = args[i+1]
				i++
				continue
			}
			msgParts = append(msgParts, args[i])
		}
		if len(msgParts) == 0 {
			fmt.Fprintln(os.Stderr, "usage: dispatch notify [--job NAME] <message...>")
			os.Exit(1)
		}
		message := strings.Join(msgParts, " ")

		notifyOn := cfg.Notify.On
		if notifyOn == "" {
			notifyOn = "always"
		}
		notifyCfg := notify.NotifyConfig{
			On:             notifyOn,
			DiscordWebhook: cfg.Notify.Discord.Webhook,
			NtfyURL:        cfg.Notify.Ntfy.URL,
			NtfyTopic:      cfg.Notify.Ntfy.Topic,
			NtfyToken:      cfg.Notify.Ntfy.Token,
			NtfyPriority:   cfg.Notify.Ntfy.Priority,
		}
		if err := notify.SendLiveNotification(message, jobName, notifyCfg); err != nil {
			fmt.Fprintf(os.Stderr, "notify: %v\n", err)
			os.Exit(1)
		}
		return
	}
```

- [ ] **Step 3: Build and verify**

Run: `go build -o dispatch . && ./dispatch notify --help 2>&1 || true`
Expected: builds without errors. The `notify` command with no args prints usage.

- [ ] **Step 4: Commit**

```bash
git add main.go
git commit -m "feat: add notify subcommand for live notifications from jobs"
```

---

### Task 4: Inject DISPATCH_JOB environment variable

**Files:**
- Modify: `main.go`

- [ ] **Step 1: Add DISPATCH_JOB to dispatch() loop**

In `main.go`, in the `dispatch()` function (line 649), change:

```go
		rc, elapsed, output := runner.RunJob(conn, job, nil, nil)
```

to:

```go
		jobEnv := []string{"DISPATCH_JOB=" + name}
		rc, elapsed, output := runner.RunJob(conn, job, nil, jobEnv)
```

- [ ] **Step 2: Add DISPATCH_JOB to `run` command (non-adhoc path)**

In `main.go`, in the `case "run":` block (line 561), change:

```go
		job := cfg.Jobs[args[0]]
		extraEnv, extraArgs := parseJobArgs(args[1:])
		rc, elapsed, output := runner.RunJob(conn, job, extraArgs, extraEnv)
```

to:

```go
		job := cfg.Jobs[args[0]]
		extraEnv, extraArgs := parseJobArgs(args[1:])
		extraEnv = append(extraEnv, "DISPATCH_JOB="+args[0])
		rc, elapsed, output := runner.RunJob(conn, job, extraArgs, extraEnv)
```

- [ ] **Step 3: Add DISPATCH_JOB to `run` command (adhoc path)**

In `main.go`, in the adhoc `run` block (line 536), change:

```go
		extraEnv, extraArgs := parseJobArgs(args[1:])
		rc, _, _ := runner.RunJob(conn, job, extraArgs, extraEnv)
```

to:

```go
		extraEnv, extraArgs := parseJobArgs(args[1:])
		extraEnv = append(extraEnv, "DISPATCH_JOB="+args[0])
		rc, _, _ := runner.RunJob(conn, job, extraArgs, extraEnv)
```

- [ ] **Step 4: Add DISPATCH_JOB to `run-all` command**

In `main.go`, in the `case "run-all":` block (line 574), change:

```go
			rc, elapsed, output := runner.RunJob(conn, job, nil, nil)
```

to:

```go
			jobEnv := []string{"DISPATCH_JOB=" + name}
			rc, elapsed, output := runner.RunJob(conn, job, nil, jobEnv)
```

- [ ] **Step 5: Build and verify**

Run: `go build -o dispatch .`
Expected: builds without errors.

- [ ] **Step 6: Run full test suite**

Run: `go test ./... -v`
Expected: all tests pass.

- [ ] **Step 7: Commit**

```bash
git add main.go
git commit -m "feat: inject DISPATCH_JOB env var into job subprocesses"
```

---

### Task 5: Update README

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Add `notify` to the Usage commands list**

In `README.md`, find the Usage section command list (around line 268). Add after the `dispatch watch <job>` line:

```
dispatch notify "msg"    # send a live notification (from inside a job)
```

- [ ] **Step 2: Add Live Notifications section**

In `README.md`, add a new section after the "Notifications" section's "Per-job overrides" subsection (after line 363). Add before the "## Analytics" section:

```markdown
### Live notifications

Jobs can send notifications in real time while they're running, using the `dispatch notify` command:

```bash
dispatch notify "Backup 50% complete"
dispatch notify --job mybackup "Step 3 done"
```

When a job is launched by the dispatcher, the `DISPATCH_JOB` environment variable is set automatically. The `notify` command reads it to tag notifications with the job name — no `--job` flag needed:

```yaml
jobs:
  etl-pipeline:
    command: python3 scripts/etl.py
    interval: 1d
    shell: /bin/bash
```

```python
# scripts/etl.py
import subprocess

subprocess.run(["dispatch", "notify", "Starting extraction..."])
# ... do work ...
subprocess.run(["dispatch", "notify", "Extraction complete, loading 50k rows"])
# ... do more work ...
subprocess.run(["dispatch", "notify", "Pipeline finished"])
```

Live notifications appear on Discord with a blurple embed and on ntfy with a speech balloon tag, visually distinct from the post-run summaries.
```

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: add live notifications to README"
```

---

### Task 6: Final verification

**Files:** none (verification only)

- [ ] **Step 1: Run full test suite**

Run: `go test ./... -v`
Expected: all tests pass.

- [ ] **Step 2: Build clean binary**

Run: `go build -o dispatch .`
Expected: builds without errors.

- [ ] **Step 3: Verify notify command works with no config**

Run: `./dispatch notify "test" 2>&1`
Expected: exits with error about no notification channels configured (since test env has no webhook).

- [ ] **Step 4: Verify DISPATCH_JOB env var is read**

Run: `DISPATCH_JOB=mytest ./dispatch notify "hello" 2>&1`
Expected: same channel error, but confirms it doesn't crash when env var is set.

- [ ] **Step 5: Verify usage output**

Run: `./dispatch --help`
Expected: `notify` command appears in the command list.
