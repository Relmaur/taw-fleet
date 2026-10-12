package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/mcp"
	"github.com/Relmaur/taw-fleet/internal/scan"
	"github.com/Relmaur/taw-fleet/internal/site"
	"github.com/Relmaur/taw-fleet/internal/tawcmd"
)

func newMCPCmd(info BuildInfo, d Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Serve taw-fleet to AI apps over MCP (Claude Code, Claude Desktop, Cursor)",
		Long: "Runs a Model Context Protocol server on stdin/stdout, for an AI app to start. Each tool is a\n" +
			"taw-fleet command you can run yourself (taw-fleet mcp tools lists them), and answers with that\n" +
			"command's --json. Read-only for now: sites, one site, doctor, pull requests, client comments,\n" +
			"production, and a theme's read-only taw commands.\n\n" +
			"Add it once:\n" +
			"  claude mcp add taw-fleet -- taw-fleet mcp\n" +
			"or, in an app's MCP settings: {\"command\": \"taw-fleet\", \"args\": [\"mcp\"]}",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return mcp.NewServer(d.mcpDeps(info)).Run(cmd.Context(), &sdk.StdioTransport{})
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "tools",
		Short: "The tools taw-fleet mcp serves, and the command each one is",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "TOOL\tSAME AS\tDASHBOARD")
			for _, t := range mcp.Tools {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", t.Name, t.Command, t.Key)
			}
			_, _ = fmt.Fprintln(w, "\nPROMPT\tSAME AS\tDASHBOARD")
			_, _ = fmt.Fprintln(w, "update_theme\ttaw-fleet handoff <site>\th")
			return w.Flush()
		},
	})
	return cmd
}

// mcpDeps runs taw-fleet's own commands in-process, with their output
// captured: stdout is the MCP connection, so nothing may reach it.
func (d Deps) mcpDeps(info BuildInfo) mcp.Deps {
	return mcp.Deps{
		Version: info.Version,
		Fleet: func(ctx context.Context, args []string) (string, error) {
			var out, errb bytes.Buffer
			inner := d
			inner.Out, inner.Err, inner.In, inner.Interactive = &out, &errb, strings.NewReader(""), false
			root := NewRoot(info, inner)
			root.SetArgs(args)
			root.SilenceUsage, root.SilenceErrors = true, true
			if err := root.ExecuteContext(ctx); err != nil {
				if msg := strings.TrimSpace(errb.String()); msg != "" {
					return out.String(), fmt.Errorf("%w\n%s", err, msg)
				}
				return out.String(), err
			}
			return out.String(), nil
		},
		Taw: func(ctx context.Context, theme string, args []string) (string, error) {
			var hints bytes.Buffer
			env := tawcmd.Env{
				Paths: d.Paths,
				Getwd: os.Getwd,
				Sites: func(ctx context.Context) ([]site.Site, error) { return scan.NewLocalSource(d.Paths).Sites(ctx) },
				Err:   &hints,
			}
			c, err := tawcmd.Resolve(ctx, env, append([]string{"@" + strings.TrimPrefix(theme, "@")}, args...))
			if err != nil {
				return "", err
			}
			res, err := d.Runner.Run(ctx, exec.Spec{Dir: c.Dir, Name: c.Path, Args: c.Args[1:], Env: c.Env})
			if err != nil {
				return "", err
			}
			out := hints.String() + string(res.Stdout)
			if e := strings.TrimSpace(string(res.Stderr)); e != "" {
				out += "\n" + e
			}
			if res.Code != 0 {
				return out, fmt.Errorf("taw @%s %s exited %d", theme, strings.Join(args, " "), res.Code)
			}
			return out, nil
		},
	}
}
