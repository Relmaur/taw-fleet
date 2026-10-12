package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/mcp"
	"github.com/Relmaur/taw-fleet/internal/scan"
	"github.com/Relmaur/taw-fleet/internal/site"
	"github.com/Relmaur/taw-fleet/internal/taw"
	"github.com/Relmaur/taw-fleet/internal/tawcmd"
)

func newMCPCmd(info BuildInfo, d Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Serve taw-fleet to AI apps over MCP (Claude Code, Claude Desktop, Cursor)",
		Long: "Runs a Model Context Protocol server on stdin/stdout, for an AI app to start. Each tool is a\n" +
			"taw-fleet command you can run yourself (taw-fleet mcp tools lists them), and answers with that\n" +
			"command's --json: sites, one site, doctor, pull requests, client comments, production, and a\n" +
			"theme's own taw commands. Tools that change something (update, merge, back to the site's\n" +
			"branch, start/stop, apply the scaffold, pull content, a theme command that writes) ask\n" +
			"first: the first call returns the dashboard's question and a one-time token, and only a\n" +
			"second call with that token runs it.\n\n" +
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
			_, _ = fmt.Fprintln(w, "\nASKS FIRST\tSAME AS\tDASHBOARD")
			for _, t := range mcp.WriteTools {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", t.Name, t.Command, t.Key)
			}
			_, _ = fmt.Fprintln(w, "\nPROMPT\tSAME AS\tDASHBOARD")
			_, _ = fmt.Fprintln(w, "update_theme\ttaw-fleet handoff <site>\th")
			_, _ = fmt.Fprintln(w, "resolve_comments\ttaw-fleet comments <site> --prompt\tX")
			return w.Flush()
		},
	})
	return cmd
}

// inProcess runs a taw-fleet command in this process and returns its
// stdout: stdout is the MCP connection, so nothing may reach it. confirm,
// when set, answers its y/N question; progress gets its stderr lines.
func (d Deps) inProcess(ctx context.Context, info BuildInfo, args []string, confirm func(string) (bool, error), progress func(string)) (string, error) {
	var out, errb bytes.Buffer
	inner := d
	inner.Out, inner.Err, inner.In, inner.Interactive, inner.Confirm = &out, &errb, strings.NewReader(""), false, confirm
	if progress != nil {
		lines := taw.NewLineWriter(progress)
		defer lines.Flush()
		inner.Err = io.MultiWriter(&errb, lines)
	}
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
}

// mcpDeps runs taw-fleet's own commands in-process, with their output
// captured: stdout is the MCP connection, so nothing may reach it.
func (d Deps) mcpDeps(info BuildInfo) mcp.Deps {
	return mcp.Deps{
		Version: info.Version,
		Fleet: func(ctx context.Context, args []string) (string, error) {
			return d.inProcess(ctx, info, args, nil, nil)
		},
		Ask: func(ctx context.Context, args []string) (string, string, error) {
			var question string
			out, err := d.inProcess(ctx, info, args, func(q string) (bool, error) {
				question = q
				return false, nil // the question only: nothing changes
			}, nil)
			if question != "" {
				// The command printed "Nothing changed." after the no: not news.
				out = strings.TrimSuffix(strings.TrimSpace(out), "Nothing changed.")
				return question, out, nil
			}
			return "", out, err
		},
		Do: func(ctx context.Context, args []string, progress func(string)) (string, error) {
			return d.inProcess(ctx, info, args, nil, progress)
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
