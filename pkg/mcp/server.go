package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/nowa277/wechat_mcp/pkg/bridge"
)

type Server struct {
	client *bridge.Client
}

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *Error      `json:"error,omitempty"`
}

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type TextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type CallToolResult struct {
	Content []TextContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"inputSchema"`
	Annotations interface{} `json:"annotations,omitempty"`
}

func NewServer(client *bridge.Client) *Server {
	return &Server{client: client}
}

func (s *Server) ServeStdio() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		cancel()
	}()

	reader := bufio.NewReader(os.Stdin)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		if len(line) == 0 || (len(line) == 1 && line[0] == '\n') {
			continue
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			s.sendError(nil, -32700, "Parse error")
			continue
		}

		s.handleRequest(ctx, req)
	}
}

func (s *Server) handleRequest(ctx context.Context, req Request) {
	switch req.Method {
	case "initialize":
		s.sendResult(req.ID, map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"serverInfo": map[string]string{
				"name":    "wechat_mcp_go",
				"version": "1.0.0",
			},
			"capabilities": map[string]interface{}{
				"tools": map[string]bool{
					"listChanged": true,
				},
			},
		})
	case "notifications/initialized":
		// No response required for notifications
	case "ping":
		s.sendResult(req.ID, map[string]interface{}{})
	case "tools/list":
		s.sendResult(req.ID, map[string]interface{}{
			"tools": s.getTools(),
		})
	case "tools/call":
		var callParams struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &callParams); err != nil {
			s.sendError(req.ID, -32602, "Invalid params")
			return
		}
		res := s.handleToolCall(ctx, callParams.Name, callParams.Arguments)
		s.sendResult(req.ID, res)
	default:
		if req.ID != nil {
			s.sendError(req.ID, -32601, fmt.Sprintf("Method not found: %s", req.Method))
		}
	}
}

func (s *Server) getTools() []Tool {
	readOnly := map[string]bool{"readOnlyHint": true}

	return []Tool{
		{
			Name:        "list_sessions",
			Description: "List chat sessions with pagination and optional keyword filtering",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"keyword": map[string]interface{}{"type": "string", "description": "Optional search keyword"},
					"limit":   map[string]interface{}{"type": "integer", "description": "Number of sessions (1-200, default 50)"},
					"offset":  map[string]interface{}{"type": "integer", "description": "Offset pagination (default 0)"},
				},
			},
			Annotations: readOnly,
		},
		{
			Name:        "list_messages",
			Description: "List messages in a chat session with pagination, order, and time bounds",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"sessionId": map[string]interface{}{"type": "string", "description": "Chatroom or user ID"},
					"limit":     map[string]interface{}{"type": "integer", "description": "Number of messages (1-200, default 50)"},
					"offset":    map[string]interface{}{"type": "integer", "description": "Offset pagination (default 0)"},
					"order":     map[string]interface{}{"type": "string", "enum": []string{"asc", "desc"}, "description": "Sort order (default asc)"},
					"since":     map[string]interface{}{"type": "integer", "description": "Start timestamp in seconds"},
					"end":       map[string]interface{}{"type": "integer", "description": "End timestamp in seconds"},
				},
				"required": []string{"sessionId"},
			},
			Annotations: readOnly,
		},
		{
			Name:        "search_messages",
			Description: "Search messages by keyword across all sessions or within a specific session",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"keyword":   map[string]interface{}{"type": "string", "description": "Keyword to search"},
					"sessionId": map[string]interface{}{"type": "string", "description": "Optional session ID to limit search"},
					"limit":     map[string]interface{}{"type": "integer", "description": "Number of messages (1-200, default 50)"},
					"offset":    map[string]interface{}{"type": "integer", "description": "Offset pagination (default 0)"},
					"since":     map[string]interface{}{"type": "integer", "description": "Start timestamp in seconds"},
					"end":       map[string]interface{}{"type": "integer", "description": "End timestamp in seconds"},
				},
				"required": []string{"keyword"},
			},
			Annotations: readOnly,
		},
		{
			Name:        "get_contact",
			Description: "Get contact details by exact username",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"username": map[string]interface{}{"type": "string", "description": "WeChat ID or username"},
				},
				"required": []string{"username"},
			},
			Annotations: readOnly,
		},
		{
			Name:        "list_files",
			Description: "List received files/documents (PDF, Word, Excel, PPT, etc.) stored locally by WeChat",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"keyword": map[string]interface{}{"type": "string", "description": "Optional file name filter"},
					"ext":     map[string]interface{}{"type": "string", "description": "Optional file extension (e.g. 'pdf', 'docx', 'xlsx')"},
					"limit":   map[string]interface{}{"type": "integer", "description": "Number of files (1-200, default 50)"},
					"offset":  map[string]interface{}{"type": "integer", "description": "Offset pagination (default 0)"},
				},
			},
			Annotations: readOnly,
		},
		{
			Name:        "get_message_file",
			Description: "Get the local absolute filesystem path and details for a specific WeChat file/document",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"fileName": map[string]interface{}{"type": "string", "description": "Exact or partial file name"},
				},
				"required": []string{"fileName"},
			},
			Annotations: readOnly,
		},
	}
}

func (s *Server) handleToolCall(ctx context.Context, name string, args map[string]interface{}) CallToolResult {
	getInt := func(k string, def int) int {
		if val, ok := args[k].(float64); ok {
			return int(val)
		}
		return def
	}
	getInt64 := func(k string) int64 {
		if val, ok := args[k].(float64); ok {
			return int64(val)
		}
		return 0
	}
	getString := func(k string) string {
		if val, ok := args[k].(string); ok {
			return val
		}
		return ""
	}

	switch name {
	case "list_sessions":
		keyword := getString("keyword")
		limit := getInt("limit", 50)
		offset := getInt("offset", 0)
		res, err := s.client.ListSessions(ctx, keyword, limit, offset)
		if err != nil {
			return formatError(err)
		}
		return formatJSON(res)

	case "list_messages":
		sessionID := getString("sessionId")
		if sessionID == "" {
			return formatError(fmt.Errorf("sessionId is required"))
		}
		limit := getInt("limit", 50)
		offset := getInt("offset", 0)
		order := getString("order")
		since := getInt64("since")
		end := getInt64("end")
		res, err := s.client.ListMessages(ctx, sessionID, limit, offset, order, since, end)
		if err != nil {
			return formatError(err)
		}
		return formatJSON(res)

	case "search_messages":
		keyword := getString("keyword")
		if keyword == "" {
			return formatError(fmt.Errorf("keyword is required"))
		}
		sessionID := getString("sessionId")
		limit := getInt("limit", 50)
		offset := getInt("offset", 0)
		since := getInt64("since")
		end := getInt64("end")
		res, err := s.client.SearchMessages(ctx, keyword, sessionID, limit, offset, since, end)
		if err != nil {
			return formatError(err)
		}
		return formatJSON(res)

	case "get_contact":
		username := getString("username")
		if username == "" {
			return formatError(fmt.Errorf("username is required"))
		}
		res, err := s.client.GetContact(ctx, username)
		if err != nil {
			return formatError(err)
		}
		return formatJSON(res)

	case "list_files":
		keyword := getString("keyword")
		ext := getString("ext")
		limit := getInt("limit", 50)
		offset := getInt("offset", 0)
		filesList, hasMore, err := s.client.ListFiles(ctx, keyword, ext, limit, offset)
		if err != nil {
			return formatError(err)
		}
		nextOffset := 0
		if hasMore {
			nextOffset = offset + len(filesList)
		}
		return formatJSON(map[string]interface{}{
			"files":      filesList,
			"hasMore":    hasMore,
			"nextOffset": nextOffset,
		})

	case "get_message_file":
		fileName := getString("fileName")
		if fileName == "" {
			return formatError(fmt.Errorf("fileName is required"))
		}
		fileInfo, err := s.client.GetFile(ctx, fileName)
		if err != nil {
			return formatError(err)
		}
		return formatJSON(fileInfo)

	default:
		return formatError(fmt.Errorf("unknown tool: %s", name))
	}
}

func formatJSON(v interface{}) CallToolResult {
	b, _ := json.MarshalIndent(v, "", "  ")
	return CallToolResult{
		Content: []TextContent{
			{Type: "text", Text: string(b)},
		},
	}
}

func formatError(err error) CallToolResult {
	msg := err.Error()
	errData, _ := json.Marshal(map[string]interface{}{
		"error": map[string]string{
			"message": msg,
		},
	})
	return CallToolResult{
		Content: []TextContent{
			{Type: "text", Text: string(errData)},
		},
		IsError: true,
	}
}

func (s *Server) sendResult(id interface{}, result interface{}) {
	resp := Response{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	b, _ := json.Marshal(resp)
	os.Stdout.Write(append(b, '\n'))
}

func (s *Server) sendError(id interface{}, code int, message string) {
	resp := Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &Error{Code: code, Message: message},
	}
	b, _ := json.Marshal(resp)
	os.Stdout.Write(append(b, '\n'))
}
