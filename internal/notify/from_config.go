package notify

import "github.com/blindly/dispatcher/internal/config"

// FromConfig maps the YAML notify block onto the transport-level config,
// defaulting the notification policy to "always".
func FromConfig(cfg config.NotifyConfig) NotifyConfig {
	on := cfg.On
	if on == "" {
		on = "always"
	}
	return NotifyConfig{
		On:             on,
		DiscordWebhook: cfg.Discord.Webhook,
		NtfyURL:        cfg.Ntfy.URL,
		NtfyTopic:      cfg.Ntfy.Topic,
		NtfyToken:      cfg.Ntfy.Token,
		NtfyPriority:   cfg.Ntfy.Priority,
	}
}
