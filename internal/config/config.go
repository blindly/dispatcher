package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type JobConfig struct {
	Name            string
	Commands        []string          // one or more commands to run in sequence
	IntervalSeconds int
	Description     string            `yaml:"description"`
	ActiveHours     *[2]int           `yaml:"-"`
	ActiveDays      *[7]bool          `yaml:"-"` // indexed by time.Weekday: Sun=0..Sat=6; nil = every day
	DependsOn       string            `yaml:"depends_on"`
	Retries         int               `yaml:"retries"`
	RetryDelay      int               `yaml:"-"` // seconds, parsed from retry_delay
	Timeout         int               `yaml:"-"` // seconds, parsed from timeout
	Adhoc           bool
	Dir             string            `yaml:"dir"`
	Env             map[string]string `yaml:"env"`
	Shell           string            `yaml:"shell"`
	Notify          string            `yaml:"notify"` // "always", "failure", or "" (inherit global)
	Paused          bool              `yaml:"paused"`
}

type DiscordConfig struct {
	Webhook string `yaml:"webhook"`
}

type NtfyConfig struct {
	URL      string `yaml:"url"`
	Topic    string `yaml:"topic"`
	Token    string `yaml:"token"`
	Priority string `yaml:"priority"`
}

type NotifyConfig struct {
	On      string        `yaml:"on"` // "always" (default) or "failure"
	Discord DiscordConfig `yaml:"discord"`
	Ntfy    NtfyConfig    `yaml:"ntfy"`
}

type DispatcherConfig struct {
	Timezone     string            `yaml:"timezone"`
	Notify       NotifyConfig      `yaml:"notify"`
	Jobs         map[string]*JobConfig
	Schedule     string            `yaml:"schedule"`
	Retention    int               `yaml:"-"` // days, parsed from retention
	PauseTimeout int              `yaml:"-"` // seconds, parsed from pause_timeout
	Vars         map[string]string `yaml:"vars"`
}

// stringOrList handles YAML values that can be either a string or list of strings.
type stringOrList []string

func (s *stringOrList) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		*s = []string{value.Value}
		return nil
	}
	var list []string
	if err := value.Decode(&list); err != nil {
		return err
	}
	*s = list
	return nil
}

// rawJob is the intermediate YAML structure before parsing.
type rawJob struct {
	Command     stringOrList      `yaml:"command"`
	Interval    string            `yaml:"interval"`
	Description string            `yaml:"description"`
	ActiveHours []int             `yaml:"active_hours"`
	Days        stringOrList      `yaml:"days"`
	DependsOn   string            `yaml:"depends_on"`
	Retries     *int              `yaml:"retries"`
	RetryDelay  string            `yaml:"retry_delay"`
	Timeout     string            `yaml:"timeout"`
	Adhoc       bool              `yaml:"adhoc"`
	Dir         string            `yaml:"dir"`
	Env         map[string]string `yaml:"env"`
	Shell       string            `yaml:"shell"`
	Notify      string            `yaml:"notify"`
	Paused      bool              `yaml:"paused"`
}

type rawConfig struct {
	Timezone       string            `yaml:"timezone"`
	Notify         NotifyConfig      `yaml:"notify"`
	Jobs           map[string]rawJob `yaml:"jobs"`
	Schedule       string            `yaml:"schedule"`
	Retention      string            `yaml:"retention"`
	PauseTimeout   string            `yaml:"pause_timeout"`
	DiscordWebhook string            `yaml:"discord_webhook"`
	Vars           map[string]string `yaml:"vars"`
}

var dayNames = map[string]int{
	"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
}

// parseDays parses a list of day names or a single keyword (weekdays / weekends / all)
// into a [7]bool indexed by time.Weekday. Returns nil for empty input ("every day").
func parseDays(days []string) (*[7]bool, error) {
	if len(days) == 0 {
		return nil, nil
	}
	var out [7]bool
	if len(days) == 1 {
		switch strings.ToLower(strings.TrimSpace(days[0])) {
		case "weekdays":
			out = [7]bool{false, true, true, true, true, true, false}
			return &out, nil
		case "weekends":
			out = [7]bool{true, false, false, false, false, false, true}
			return &out, nil
		case "all":
			out = [7]bool{true, true, true, true, true, true, true}
			return &out, nil
		}
	}
	for _, d := range days {
		key := strings.ToLower(strings.TrimSpace(d))
		if _, isKeyword := map[string]bool{"weekdays": true, "weekends": true, "all": true}[key]; isKeyword {
			return nil, fmt.Errorf("days: keyword %q must be standalone, not combined with other entries", key)
		}
		idx, ok := dayNames[key]
		if !ok {
			return nil, fmt.Errorf("days: unknown day %q (want sun mon tue wed thu fri sat or weekdays/weekends/all)", d)
		}
		out[idx] = true
	}
	return &out, nil
}

var intervalRe = regexp.MustCompile(`^(\d+)([smhdw])$`)

func ParseInterval(s string) (int, error) {
	s = strings.TrimSpace(s)
	m := intervalRe.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("invalid interval: %q (expected e.g. 5m, 2h, 1d)", s)
	}
	value, _ := strconv.Atoi(m[1])
	multipliers := map[string]int{"s": 1, "m": 60, "h": 3600, "d": 86400, "w": 604800}
	return value * multipliers[m[2]], nil
}

// ExpandVars replaces {{.KEY}} with values from the vars map.
func ExpandVars(text string, vars map[string]string) string {
	for k, v := range vars {
		text = strings.ReplaceAll(text, "{{."+k+"}}", v)
	}
	return text
}

// LoadDotEnv reads a .env file and sets the variables in the process environment.
// Checks .dispatcher/.env first, then falls back to dir/.env for backward compatibility.
func LoadDotEnv(dir string) {
	envPath := dir + "/.dispatcher/.env"
	data, err := os.ReadFile(envPath)
	if err != nil {
		// Fallback to old location (project root)
		envPath = dir + "/.env"
		data, err = os.ReadFile(envPath)
		if err != nil {
			return // .env is optional
		}
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		// Strip surrounding quotes
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		os.Setenv(key, value)
	}
}

func ExpandEnv(text string) string {
	re := regexp.MustCompile(`\$\{(\w+)\}`)
	return re.ReplaceAllStringFunc(text, func(match string) string {
		varName := re.FindStringSubmatch(match)[1]
		return os.Getenv(varName)
	})
}

// ExtractNotifySettings does a lenient parse of the config file to extract only the
// global notify block. It's used when Load() fails — we still want to notify about
// config errors if notification channels are configured.
func ExtractNotifySettings(path string) *NotifyConfig {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	expanded := ExpandEnv(string(data))

	// Lenient parse — just extract the notify block and ignore unknown keys.
	var raw struct {
		Notify NotifyConfig `yaml:"notify"`
	}
	if err := yaml.Unmarshal([]byte(expanded), &raw); err != nil {
		return nil
	}

	// Check if any notification channel is configured
	cfg := &raw.Notify
	if cfg.Discord.Webhook == "" && cfg.Ntfy.URL == "" && cfg.Ntfy.Topic == "" {
		return nil
	}
	return cfg
}

func Load(path string) (*DispatcherConfig, error) {
	// Load .env from config directory before expanding variables
	configDir := path
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		configDir = path[:idx]
	} else {
		configDir = "."
	}
	LoadDotEnv(configDir)

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	expanded := ExpandEnv(string(data))

	var raw rawConfig
	dec := yaml.NewDecoder(strings.NewReader(expanded))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	vars := raw.Vars
	if vars == nil {
		vars = make(map[string]string)
	}

	schedule := raw.Schedule
	if schedule == "" {
		schedule = "*/5 * * * *"
	}

	retention := 90 // default 90 days
	if raw.Retention != "" {
		retSec, err := ParseInterval(raw.Retention)
		if err != nil {
			return nil, fmt.Errorf("retention: %w", err)
		}
		retention = retSec / 86400 // convert to days
		if retention < 1 {
			retention = 1
		}
	}

	pauseTimeout := 3600 // default 1h
	if raw.PauseTimeout != "" {
		pt, err := ParseInterval(raw.PauseTimeout)
		if err != nil {
			return nil, fmt.Errorf("pause_timeout: %w", err)
		}
		pauseTimeout = pt
	}

	cfg := &DispatcherConfig{
		Timezone:     raw.Timezone,
		Notify:       raw.Notify,
		Jobs:         make(map[string]*JobConfig),
		Schedule:     schedule,
		Retention:    retention,
		PauseTimeout: pauseTimeout,
		Vars:         vars,
	}

	if cfg.Notify.Discord.Webhook == "" && raw.DiscordWebhook != "" {
		cfg.Notify.Discord.Webhook = raw.DiscordWebhook
	}

	if cfg.Timezone == "" {
		cfg.Timezone = "America/New_York"
	}

	for name, rj := range raw.Jobs {
		var intervalSec int
		if rj.Interval != "" {
			var err error
			intervalSec, err = ParseInterval(rj.Interval)
			if err != nil {
				return nil, fmt.Errorf("job %q: %w", name, err)
			}
		} else if !rj.Adhoc {
			return nil, fmt.Errorf("job %q: interval is required (unless adhoc: true)", name)
		}

		commands := make([]string, len(rj.Command))
		for i, cmd := range rj.Command {
			commands[i] = ExpandVars(cmd, vars)
		}

		job := &JobConfig{
			Name:            name,
			Commands:        commands,
			IntervalSeconds: intervalSec,
			Description:     rj.Description,
			DependsOn:       rj.DependsOn,
			Retries:         2, // default
			RetryDelay:      5, // default 5s
			Adhoc:           rj.Adhoc,
			Dir:             rj.Dir,
			Env:             rj.Env,
			Shell:           rj.Shell,
			Notify:          rj.Notify,
			Paused:          rj.Paused,
		}

		if rj.Retries != nil {
			job.Retries = *rj.Retries
		}

		if rj.RetryDelay != "" {
			delay, err := ParseInterval(rj.RetryDelay)
			if err != nil {
				return nil, fmt.Errorf("job %q retry_delay: %w", name, err)
			}
			job.RetryDelay = delay
		}

		job.Timeout = 600 // default 600s

		if rj.Timeout != "" {
			timeout, err := ParseInterval(rj.Timeout)
			if err != nil {
				return nil, fmt.Errorf("job %q timeout: %w", name, err)
			}
			job.Timeout = timeout
		}

		if len(rj.ActiveHours) == 2 {
			job.ActiveHours = &[2]int{rj.ActiveHours[0], rj.ActiveHours[1]}
		}

		activeDays, err := parseDays(rj.Days)
		if err != nil {
			return nil, fmt.Errorf("job %q: %w", name, err)
		}
		job.ActiveDays = activeDays

		cfg.Jobs[name] = job
	}

	return cfg, nil
}
