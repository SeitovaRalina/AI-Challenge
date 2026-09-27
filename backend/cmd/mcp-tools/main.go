// Command mcp-tools connects to the configured public MCP server, performs
// the initialize handshake, and prints the tools it exposes — the day-16
// assignment in its most literal form, sharing the exact client code the
// backend's «Источники» screen uses.
//
//	cd backend
//	go run ./cmd/mcp-tools            # summary table
//	go run ./cmd/mcp-tools -params    # plus every tool's input parameters
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/joho/godotenv"

	"aiwork/backend/internal/mcpclient"
)

func main() {
	showParams := flag.Bool("params", false, "print each tool's input parameters")
	flag.Parse()

	// Same .env the backend reads, so GITHUB_TOKEN only lives in one place.
	_ = godotenv.Load()

	exitCode := 0
	for _, cfg := range mcpclient.DefaultServers() {
		fmt.Printf("→ %s\n  %s\n", cfg.Name, cfg.URL)
		result, err := mcpclient.ListTools(context.Background(), cfg)
		if err != nil {
			fmt.Printf("  ✗ connection failed: %v\n", err)
			var mcpErr *mcpclient.Error
			if errors.As(err, &mcpErr) && mcpErr.Kind == mcpclient.ErrNoToken {
				fmt.Printf("    set %s in backend/.env\n", cfg.TokenEnv)
			}
			exitCode = 1
			continue
		}

		fmt.Printf("  ✓ connected: %s %s, protocol %s, %dms\n",
			result.ServerName, result.ServerVersion, result.ProtocolVersion, result.DurationMs)
		fmt.Printf("  %d tool(s):\n\n", len(result.Tools))

		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  #\tTOOL\tRO\tDESCRIPTION")
		for i, t := range result.Tools {
			ro := ""
			if t.ReadOnly {
				ro = "ro"
			}
			fmt.Fprintf(tw, "  %d\t%s\t%s\t%s\n", i+1, t.Name, ro, firstLine(t.Description, 80))
		}
		tw.Flush()

		if *showParams {
			for _, t := range result.Tools {
				fmt.Printf("\n  %s\n", t.Name)
				for _, p := range t.Params {
					req := ""
					if p.Required {
						req = " (required)"
					}
					fmt.Printf("    - %s: %s%s — %s\n", p.Name, p.Type, req, firstLine(p.Description, 70))
				}
			}
		}
		fmt.Println()
	}
	os.Exit(exitCode)
}

func firstLine(s string, max int) string {
	s = strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
	if r := []rune(s); len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}
