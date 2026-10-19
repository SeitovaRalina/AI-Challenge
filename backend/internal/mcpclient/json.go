package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
)

// CallJSON is the shared typed boundary for Worklog and Solidtime tools.
func CallJSON(ctx context.Context, conn *Conn, tool string, args map[string]any, out any) error {
	if conn == nil {
		return fmt.Errorf("%s: MCP connection unavailable", tool)
	}
	result, err := conn.Call(ctx, tool, args)
	if err != nil {
		return err
	}
	if result.IsError {
		return fmt.Errorf("%s", result.Text)
	}
	raw := result.Structured
	if len(raw) == 0 {
		raw = json.RawMessage(result.Text)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s: decode result: %w", tool, err)
	}
	return nil
}
