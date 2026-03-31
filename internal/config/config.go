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
	Commands        []string // one or more commands to run in sequence
	IntervalSeconds int
	Description     string  `yaml:"description"`
	ActiveHours     *[2]int `yaml:"-"`
	DependsOn       string  `yaml:"depends_on"`
	Retries         int     `yaml:"retries"`
	RetryDelay      int     `yaml:"-"` // seconds, parsed from retry_delay
	Timeout         int     `yaml:"-"` // seconds, parsed from timeout
	Adhoc           bool
}

type DiscordConfig struct {
	Webhook string `yaml:"webhook"`
}

type NotifyConfig struct {
	Discord DiscordConfig `yaml:"discord"`
}

type DispatcherConfig struct {
	Timezone string            `yaml:"timezone"`
	Notify   NotifyConfig      `yaml:"notify"`
	Jobs     map[string]*JobConfig
	DbPath   string            `yaml:"db_path"`
	Vars     map[string]string `yaml:"vars"`
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
	Command     stringOrList `yaml:"command"`
	Interval    string       `yaml:"interval"`
	Description string `yaml:"description"`
	ActiveHours []int  `yaml:"active_hours"`
	DependsOn   string `yaml:"depends_on"`
	Retries     *int   `yaml:"retries"`
	RetryDelay  string `yaml:"retry_delay"`
	Timeout     string `yaml:"timeout"`
	Adhoc       bool   `yaml:"adhoc"`
}

type rawConfig struct {
	Timezone       string            `yaml:"timezone"`
	Notify         NotifyConfig      `yaml:"notify"`
	Jobs           map[string]rawJob `yaml:"jobs"`
	DbPath         string            `yaml:"db_path"`
	DiscordWebhook string            `yaml:"discord_webhook"`
	Vars           map[string]string `yaml:"vars"`
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

func ExpandEnv(text string) string {
	re := regexp.MustCompile(`\$\{(\w+)\}`)
	return re.ReplaceAllStringFunc(text, func(match string) string {
		varName := re.FindStringSubmatch(match)[1]
		return os.Getenv(varName)
	})
}

func Load(path string) (*DispatcherConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	expanded := ExpandEnv(string(data))

	var raw rawConfig
	if err := yaml.Unmarshal([]byte(expanded), &raw); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	vars := raw.Vars
	if vars == nil {
		vars = make(map[string]string)
	}

	cfg := &DispatcherConfig{
		Timezone: raw.Timezone,
		Notify:   raw.Notify,
		Jobs:     make(map[string]*JobConfig),
		DbPath:   raw.DbPath,
		Vars:     vars,
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

		cfg.Jobs[name] = job
	}

	return cfg, nil
}
