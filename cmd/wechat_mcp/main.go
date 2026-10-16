package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nowa277/wechat_mcp/pkg/bridge"
	"github.com/nowa277/wechat_mcp/pkg/mcp"
	"github.com/nowa277/wechat_mcp/pkg/wechat"
)

func main() {
	var (
		sanitizeFlag = flag.Bool("sanitize", false, "Enable PII masking (phones, emails, IDs, bank cards)")
		doctorFlag   = flag.Bool("doctor", false, "Run diagnostic checks on WeChat native environment")
		installFlag  = flag.String("install", "", "Generate MCP configuration for client (claude-desktop, cursor, raw)")
		portFlag     = flag.Int("port", 0, "Custom HTTP fallback port")
		tokenFlag    = flag.String("token", "", "Custom HTTP fallback token")
		execFlag     = flag.String("executable", "", "Custom executable path for client configuration")
	)

	flag.Parse()

	// 1. Doctor command
	if *doctorFlag {
		runDoctor(*portFlag, *tokenFlag)
		return
	}

	// 2. Install command
	if *installFlag != "" {
		runInstall(*installFlag, *execFlag, *sanitizeFlag)
		return
	}

	// 3. Default: run MCP stdio server
	baseURL := ""
	if *portFlag > 0 {
		baseURL = fmt.Sprintf("http://127.0.0.1:%d", *portFlag)
	}

	client, err := bridge.NewClient(bridge.ClientOptions{
		BaseURL:  baseURL,
		Token:    *tokenFlag,
		Sanitize: *sanitizeFlag,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "[wechat_mcp] Failed to initialize native engine: %v\n", err)
		os.Exit(1)
	}

	server := mcp.NewServer(client)
	if err := server.ServeStdio(); err != nil {
		fmt.Fprintf(os.Stderr, "[wechat_mcp] Server error: %v\n", err)
		os.Exit(1)
	}
}

func runDoctor(customPort int, customToken string) {
	fmt.Println("\n[wechat_mcp doctor] WeChat MCP Standalone Diagnostics (Pure Go)")
	fmt.Println("---------------------------------------------------------------")

	// 1. Detect WeChat account
	info, err := wechat.DetectAccount()
	if err != nil {
		fmt.Printf("WeChat Account:      [FAIL] %v\n", err)
	} else {
		fmt.Printf("WeChat Account:      [OK] Detected\n")
		fmt.Printf("  WxID:              %s\n", info.WxID)
		if info.Nickname != "" {
			fmt.Printf("  Nickname:          %s\n", info.Nickname)
		}
		fmt.Printf("  Data Directory:    %s\n", info.DataDir)
		if info.SessionDb != "" {
			fmt.Printf("  Session DB:        %s\n", info.SessionDb)
		}
		if info.DecryptKey != "" {
			fmt.Printf("  Database Key:      [OK] Loaded (%s...)\n", info.DecryptKey[:8])
		}
	}

	// 2. Initialize Direct Native Engine
	client, err := bridge.NewClient(bridge.ClientOptions{})
	if err != nil {
		fmt.Printf("WCDB Direct Engine:  [FAIL] %v\n", err)
		fmt.Printf("\nVerdict: UNHEALTHY\n\n")
		os.Exit(1)
	}

	fmt.Printf("WCDB Direct Engine:  [OK] Native CGO Driver Active (Zero Electron)\n")

	// 3. Test read sessions
	ctx := context.Background()
	sessions, err := client.ListSessions(ctx, "", 3, 0)
	if err != nil {
		fmt.Printf("Chat DB Query:       [FAIL] %v\n", err)
		fmt.Printf("\nVerdict: UNHEALTHY\n\n")
		os.Exit(1)
	}

	fmt.Printf("Chat DB Query:       [OK] Direct read verified\n")
	fmt.Printf("  Active Sessions:   %d returned\n", len(sessions.Sessions))
	if len(sessions.Sessions) > 0 {
		fmt.Printf("  Latest Session:    %s (%s)\n", sessions.Sessions[0].DisplayName, sessions.Sessions[0].SessionID)
	}

	fmt.Printf("\nVerdict: HEALTHY (Standalone native engine ready, completely decoupled)\n\n")
}

func runInstall(client, customExec string, sanitize bool) {
	executable := customExec
	if executable == "" {
		if exe, err := os.Executable(); err == nil {
			executable = exe
		} else {
			executable = "wechat_mcp"
		}
	}
	executable = filepath.Clean(executable)

	args := []string{}
	if sanitize {
		args = append(args, "--sanitize")
	}

	var output interface{}
	switch client {
	case "claude-desktop", "cursor":
		output = map[string]interface{}{
			"mcpServers": map[string]interface{}{
				"wechat": map[string]interface{}{
					"command": executable,
					"args":    args,
				},
			},
		}
	case "raw":
		output = map[string]interface{}{
			"command": executable,
			"args":    args,
		}
	default:
		fmt.Fprintf(os.Stderr, "Error: Unknown client %q. Supported: claude-desktop, cursor, raw\n", client)
		os.Exit(2)
	}

	b, _ := json.MarshalIndent(output, "", "  ")
	fmt.Println(string(b))
}
