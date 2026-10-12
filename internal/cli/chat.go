package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Relmaur/taw-fleet/internal/agent"
	"github.com/Relmaur/taw-fleet/internal/mcp"
	"github.com/Relmaur/taw-fleet/internal/scan"
)

// chatter starts Claude Code for the dashboard's chat: in the theme's folder
// for a site, in the fleet folder for the fleet, with taw-fleet's own MCP
// server and its read tools allowed.
func (d Deps) chatter(claude string) func(ctx context.Context, sc agent.Scope, resume string) (*agent.Session, error) {
	return func(ctx context.Context, sc agent.Scope, resume string) (*agent.Session, error) {
		dir := sc.Path
		if dir == "" {
			dir = d.Paths.FleetDir
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, err
			}
		}
		cfg, err := d.chatMCP()
		if err != nil {
			return nil, err
		}
		return agent.Start(ctx, nil, agent.Options{Claude: claude, Dir: dir, Resume: resume,
			Prompt: agent.Prompt(sc), MCP: cfg, Allow: chatReadTools()})
	}
}

// chatMCP writes the chat's MCP config: taw-fleet's own server.
func (d Deps) chatMCP() (string, error) {
	exe, err := d.exePath()
	if err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{
		agent.Server: map[string]any{"command": exe, "args": []string{"mcp"}}}}, "", "  ")
	if err != nil {
		return "", err
	}
	dir := filepath.Join(d.Paths.CacheDir, "chat")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "mcp.json")
	return path, os.WriteFile(path, b, 0o600)
}

// chatReadTools are taw-fleet's tools that only read: they never ask. The
// taw tool isn't one (it asks before a command that writes).
func chatReadTools() []string {
	var out []string
	for _, t := range mcp.Tools {
		if t.Name != "taw" {
			out = append(out, agent.ToolName(t.Name))
		}
	}
	return out
}

// chatWrites are taw-fleet's write tools by name.
func chatWrites() map[string]bool {
	w := map[string]bool{}
	for _, t := range mcp.WriteTools {
		w[t.Name] = true
	}
	return w
}

func newChatCmd(d Deps, g *globals) *cobra.Command {
	var prompt bool
	var theme string
	cmd := &cobra.Command{
		Use:   "chat [site]",
		Short: "The dashboard's chat with Claude (>), and what Claude is told",
		Long: "The dashboard's > opens a chat with your own Claude Code, about the whole fleet (from\n" +
			"the table) or one site (from its details), with taw-fleet's tools. It asks before it\n" +
			"changes anything. --prompt prints what taw-fleet adds to Claude's instructions, for\n" +
			"the fleet or a site, so you can read it or give it to another agent.",
		Example: "  taw-fleet chat --prompt\n  taw-fleet chat ls-mexico --prompt",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !prompt {
				return errors.New("open the dashboard and press > (or > on a site's details); --prompt prints the instructions")
			}
			sc := agent.Scope{}
			if len(args) == 1 {
				offline := *g
				offline.offline = true
				rep, err := d.scanner(&offline).Run(cmd.Context())
				if err != nil {
					return err
				}
				s, t, err := scan.ResolveTheme(rep.Sites, args[0], theme)
				if err != nil {
					return err
				}
				sc = agent.ScopeOf(*s, t)
			}
			_, err := fmt.Fprint(cmd.OutOrStdout(), agent.Prompt(sc))
			return err
		},
	}
	cmd.Flags().BoolVar(&prompt, "prompt", false, "print what taw-fleet tells Claude")
	cmd.Flags().StringVar(&theme, "theme", "", "the theme, when the site has several")
	return cmd
}
