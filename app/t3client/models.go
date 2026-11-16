package t3client

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// ModelSelection names the provider instance and model a thread runs with,
// and the model's options, such as its effort (see Effort).
type ModelSelection struct {
	InstanceID string          `json:"instanceId"`
	Model      string          `json:"model"`
	Options    json.RawMessage `json:"options,omitempty"`
}

// set reports whether the selection names a model.
func (m *ModelSelection) set() bool {
	return m != nil && m.InstanceID != "" && m.Model != ""
}

// serverConfig is the part of server.getConfig that lists models.
type serverConfig struct {
	Providers []configProvider `json:"providers"`
	Settings  configSettings   `json:"settings"`
}

// configProvider is one provider instance of the server's config.
type configProvider struct {
	InstanceID    string               `json:"instanceId"`
	DisplayName   string               `json:"displayName"`
	Enabled       bool                 `json:"enabled"`
	Models        []configModel        `json:"models"`
	SlashCommands []configSlashCommand `json:"slashCommands"`
}

// configModel is one model a provider offers.
type configModel struct {
	Slug         string            `json:"slug"`
	Name         string            `json:"name"`
	IsDefault    bool              `json:"isDefault"`
	Capabilities ModelCapabilities `json:"capabilities"`
}

// configSlashCommand is one slash command a provider understands.
type configSlashCommand struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Input       *configCommandInput `json:"input"`
}

// configCommandInput describes what a slash command takes after its name.
type configCommandInput struct {
	Hint string `json:"hint"`
}

// configSettings is the part of the server's settings aidiff reads.
type configSettings struct {
	DefaultModelSelection *ModelSelection `json:"defaultModelSelection"`
}

// name is how the provider is shown: its display name, else its instance id.
func (p configProvider) name() string {
	if p.DisplayName != "" {
		return p.DisplayName
	}
	return p.InstanceID
}

// defaultModel is the provider's default model, else its first, if it has any.
func (p configProvider) defaultModel() (configModel, bool) {
	for _, m := range p.Models {
		if m.IsDefault {
			return m, true
		}
	}
	if len(p.Models) == 0 {
		return configModel{}, false
	}
	return p.Models[0], true
}

func (c *Conn) fetchConfig(ctx context.Context) (serverConfig, error) {
	var cfg serverConfig
	if err := c.call(ctx, "server.getConfig", struct{}{}, &cfg); err != nil {
		return serverConfig{}, err
	}
	return cfg, nil
}

// ModelChoice is one model a thread can run with.
type ModelChoice struct {
	InstanceID   string // the provider instance offering it
	Provider     string // the provider's display name
	Slug         string
	Name         string
	Capabilities ModelCapabilities
}

// Selection is what choosing the model sends.
func (m ModelChoice) Selection() ModelSelection {
	return ModelSelection{InstanceID: m.InstanceID, Model: m.Slug}
}

// ProviderCommand is a slash command the provider itself understands; it is
// sent to the agent as a message, the way T3's own composer does.
type ProviderCommand struct {
	InstanceID  string
	Name        string // without the slash
	Description string
	Hint        string // what goes after it, if anything
}

// Catalog is what the server's enabled providers offer: models and commands.
type Catalog struct {
	Models   []ModelChoice
	Commands []ProviderCommand
}

// FetchCatalog reads the models and slash commands of the server's enabled
// providers.
func (c *Conn) FetchCatalog(ctx context.Context) (Catalog, error) {
	cfg, err := c.fetchConfig(ctx)
	if err != nil {
		return Catalog{}, err
	}
	var cat Catalog
	for _, pr := range cfg.Providers {
		if !pr.Enabled {
			continue
		}
		for _, m := range pr.Models {
			cat.Models = append(cat.Models, ModelChoice{InstanceID: pr.InstanceID, Provider: pr.name(), Slug: m.Slug, Name: m.Name, Capabilities: m.Capabilities})
		}
		for _, sc := range pr.SlashCommands {
			cmd := ProviderCommand{InstanceID: pr.InstanceID, Name: strings.TrimPrefix(sc.Name, "/"), Description: sc.Description}
			if sc.Input != nil {
				cmd.Hint = sc.Input.Hint
			}
			cat.Commands = append(cat.Commands, cmd)
		}
	}
	return cat, nil
}

// DefaultModel picks what a new thread in p runs with: the project's default
// model, else the server's, else the default (or first) model of the first
// enabled provider.
func (c *Conn) DefaultModel(ctx context.Context, p Project) (ModelSelection, error) {
	if p.DefaultModelSelection.set() {
		return *p.DefaultModelSelection, nil
	}
	cfg, err := c.fetchConfig(ctx)
	if err != nil {
		return ModelSelection{}, err
	}
	if cfg.Settings.DefaultModelSelection.set() {
		return *cfg.Settings.DefaultModelSelection, nil
	}
	for _, pr := range cfg.Providers {
		if !pr.Enabled {
			continue
		}
		if m, ok := pr.defaultModel(); ok {
			return ModelSelection{InstanceID: pr.InstanceID, Model: m.Slug}, nil
		}
	}
	return ModelSelection{}, errors.New("the T3 server has no default model: pick one in T3 Code's settings")
}
