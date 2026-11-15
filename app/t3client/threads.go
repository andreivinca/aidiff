package t3client

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// maxTitleRunes bounds a thread title taken from its first message.
const maxTitleRunes = 80

// ModelSelection names the provider instance and model a thread runs with.
type ModelSelection struct {
	InstanceID string          `json:"instanceId"`
	Model      string          `json:"model"`
	Options    json.RawMessage `json:"options,omitempty"`
}

// set reports whether the selection names a model.
func (m *ModelSelection) set() bool { return m != nil && m.InstanceID != "" && m.Model != "" }

// LaunchRequest describes a thread to start.
type LaunchRequest struct {
	ProjectID string
	Title     string // defaults to the first line of Message
	Message   string // the agent's first instruction
	Model     ModelSelection
}

// LaunchThread starts a thread in the project's folder and returns its id.
// With a message the agent starts on it at once; without one the thread
// waits empty for the first message, titled "New thread" until T3 names it
// after that message. The thread runs in T3's default runtime mode, full
// access, like one started from its CLI.
func LaunchThread(ctx context.Context, c *Conn, req LaunchRequest) (string, error) {
	msg := strings.TrimSpace(req.Message)
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = titleFrom(msg)
	}
	payload := map[string]any{
		"commandId":         newID(),
		"projectId":         req.ProjectID,
		"title":             title,
		"modelSelection":    req.Model,
		"runtimeMode":       "full-access",
		"interactionMode":   "default",
		"workspaceStrategy": map[string]any{"type": "root"},
	}
	if msg != "" {
		payload["initialMessage"] = map[string]any{"messageId": newID(), "text": msg, "attachments": []any{}}
	}
	if title == "" {
		payload["title"] = "New thread"
		payload["generateTitle"] = true
	}
	var res struct {
		ThreadID string `json:"threadId"`
	}
	if err := c.Call(ctx, "orchestration.launchThread", payload, &res); err != nil {
		return "", err
	}
	if res.ThreadID == "" {
		return "", errors.New("launchThread: the server returned no thread id")
	}
	return res.ThreadID, nil
}

// Command is one orchestration command, sent whole through Dispatch.
type Command map[string]any

// Dispatch sends an orchestration command. A commandId is added when the
// command has none.
func Dispatch(ctx context.Context, c *Conn, cmd Command) error {
	if _, ok := cmd["commandId"]; !ok {
		cmd["commandId"] = newID()
	}
	var res struct {
		Sequence int `json:"sequence"`
	}
	return c.Call(ctx, "orchestration.dispatchCommand", cmd, &res)
}

// MessageCommand hands text to the thread's agent. A busy thread gets it as
// its next turn; nothing running is interrupted.
func MessageCommand(threadID, text string) Command {
	return MessageCommandWith(threadID, newID(), text, nil)
}

// SendMessage hands text to the thread's agent; see MessageCommand.
func SendMessage(ctx context.Context, c *Conn, threadID, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("nothing to send")
	}
	return Dispatch(ctx, c, MessageCommand(threadID, text))
}

// serverConfig is the part of server.getConfig that lists models.
type serverConfig struct {
	Providers []struct {
		InstanceID  string `json:"instanceId"`
		DisplayName string `json:"displayName"`
		Enabled     bool   `json:"enabled"`
		Models      []struct {
			Slug         string            `json:"slug"`
			Name         string            `json:"name"`
			IsDefault    bool              `json:"isDefault"`
			Capabilities ModelCapabilities `json:"capabilities"`
		} `json:"models"`
		SlashCommands []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Input       *struct {
				Hint string `json:"hint"`
			} `json:"input"`
		} `json:"slashCommands"`
	} `json:"providers"`
	Settings struct {
		DefaultModelSelection *ModelSelection `json:"defaultModelSelection"`
	} `json:"settings"`
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
func FetchCatalog(ctx context.Context, c *Conn) (Catalog, error) {
	var cfg serverConfig
	if err := c.Call(ctx, "server.getConfig", struct{}{}, &cfg); err != nil {
		return Catalog{}, err
	}
	var cat Catalog
	for _, pr := range cfg.Providers {
		if !pr.Enabled {
			continue
		}
		name := pr.DisplayName
		if name == "" {
			name = pr.InstanceID
		}
		for _, m := range pr.Models {
			cat.Models = append(cat.Models, ModelChoice{InstanceID: pr.InstanceID, Provider: name, Slug: m.Slug, Name: m.Name, Capabilities: m.Capabilities})
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
func DefaultModel(ctx context.Context, c *Conn, p Project) (ModelSelection, error) {
	if p.DefaultModelSelection.set() {
		return *p.DefaultModelSelection, nil
	}
	var cfg serverConfig
	if err := c.Call(ctx, "server.getConfig", struct{}{}, &cfg); err != nil {
		return ModelSelection{}, err
	}
	if cfg.Settings.DefaultModelSelection.set() {
		return *cfg.Settings.DefaultModelSelection, nil
	}
	for _, pr := range cfg.Providers {
		if !pr.Enabled || len(pr.Models) == 0 {
			continue
		}
		slug := pr.Models[0].Slug
		for _, m := range pr.Models {
			if m.IsDefault {
				slug = m.Slug
				break
			}
		}
		return ModelSelection{InstanceID: pr.InstanceID, Model: slug}, nil
	}
	return ModelSelection{}, errors.New("the T3 server has no default model: pick one in T3 Code's settings")
}

// titleFrom is the first line of msg, shortened to a title.
func titleFrom(msg string) string {
	line, _, _ := strings.Cut(msg, "\n")
	line = strings.TrimSpace(line)
	if r := []rune(line); len(r) > maxTitleRunes {
		return strings.TrimSpace(string(r[:maxTitleRunes-1])) + "…"
	}
	return line
}

// newID returns a random UUID (version 4), the id format T3's own clients use
// for commands, messages and threads.
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("t3client: random source unavailable: " + err.Error())
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
