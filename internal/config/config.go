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
	Command         string `yaml:"command"`
	IntervalSeconds int
	Description     string  `yaml:"description"`
	ActiveHours     *[2]int `yaml:"-"`
	DependsOn       string  `yaml:"depends_on"`
	Retries         int     `yaml:"retries"`
	RetryDelay      int     `yaml:"-"` // seconds, parsed from retry_delay
}

type DiscordConfig struct {
	Webhook string `yaml:"webhook"`
}

type NotifyConfig struct {
	Discord DiscordConfig `yaml:"discord"`
}

type DispatcherConfig struct {
	Timezone string       `yaml:"timezone"`
	Notify   NotifyConfig `yaml:"notify"`
	Jobs     map[string]*JobConfig
}

// rawJob is the intermediate YAML structure before parsing.
type rawJob struct {
	Command     string `yaml:"command"`
	Interval    string `yaml:"interval"`
	Description string `yaml:"description"`
	ActiveHours []int  `yaml:"active_hours"`
	DependsOn   string `yaml:"depends_on"`
	Retries     *int   `yaml:"retries"`
	RetryDelay  string `yaml:"retry_delay"`
}

type rawConfig struct {
	Timezone string            `yaml:"timezone"`
	Notify   NotifyConfig      `yaml:"notify"`
	Jobs     map[string]rawJob `yaml:"jobs"`
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

	cfg := &DispatcherConfig{
		Timezone: raw.Timezone,
		Notify:   raw.Notify,
		Jobs:     make(map[string]*JobConfig),
	}

	if cfg.Timezone == "" {
		cfg.Timezone = "America/New_York"
	}

	for name, rj := range raw.Jobs {
		intervalSec, err := ParseInterval(rj.Interval)
		if err != nil {
			return nil, fmt.Errorf("job %q: %w", name, err)
		}

		job := &JobConfig{
			Name:            name,
			Command:         rj.Command,
			IntervalSeconds: intervalSec,
			Description:     rj.Description,
			DependsOn:       rj.DependsOn,
			Retries:         2, // default
			RetryDelay:      5, // default 5s
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

		if len(rj.ActiveHours) == 2 {
			job.ActiveHours = &[2]int{rj.ActiveHours[0], rj.ActiveHours[1]}
		}

		cfg.Jobs[name] = job
	}

	return cfg, nil
}
