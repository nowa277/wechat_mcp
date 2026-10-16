package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nowa277/wechat_mcp/pkg/files"
	"github.com/nowa277/wechat_mcp/pkg/sanitize"
	"github.com/nowa277/wechat_mcp/pkg/wcdb"
	"github.com/nowa277/wechat_mcp/pkg/wechat"
)

type Client struct {
	useNative  bool
	accountDir string
	baseURL    string
	token      string
	httpClient *http.Client
	sanitize   bool
}

type ClientOptions struct {
	BaseURL  string
	Token    string
	Sanitize bool
}

type Session struct {
	SessionID     string `json:"sessionId"`
	Username      string `json:"username"`
	DisplayName   string `json:"displayName,omitempty"`
	Type          int    `json:"type"`
	SessionType   string `json:"sessionType,omitempty"`
	LastTimestamp int64  `json:"lastTimestamp,omitempty"`
	UnreadCount   int    `json:"unreadCount"`
}

type SessionsResponse struct {
	Sessions   []Session `json:"sessions"`
	HasMore    bool      `json:"hasMore"`
	NextOffset int       `json:"nextOffset,omitempty"`
}

type Message struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Sender    string `json:"sender"`
	Timestamp int64  `json:"timestamp"`
	Type      int    `json:"type"`
	Content   string `json:"content"`
	FileName  string `json:"fileName,omitempty"`
	FilePath  string `json:"filePath,omitempty"`
	FileSize  int64  `json:"fileSize,omitempty"`
}

type MessagesResponse struct {
	Messages   []Message `json:"messages"`
	HasMore    bool      `json:"hasMore"`
	NextOffset int       `json:"nextOffset,omitempty"`
}

type Contact struct {
	Username    string `json:"username"`
	Nickname    string `json:"nickname,omitempty"`
	Remark      string `json:"remark,omitempty"`
	DisplayName string `json:"displayName"`
	Type        string `json:"type,omitempty"`
}

// NewClient creates a new bridge client, preferring direct native WCDB database access.
func NewClient(opts ClientOptions) (*Client, error) {
	c := &Client{
		httpClient: &http.Client{Timeout: 10 * time.Second},
		sanitize:   opts.Sanitize,
	}

	info, err := wechat.DetectAccount()
	if info != nil {
		c.accountDir = info.AccountDir
	}

	if err == nil && info != nil && info.SessionDb != "" && info.DecryptKey != "" {
		// Attempt native WCDB direct access without launching any Electron/WeFlow GUI
		if _, err := wcdb.OpenAccount(info.SessionDb, info.DecryptKey); err == nil {
			c.useNative = true
			return c, nil
		}
	}

	// Fallback to HTTP API if configured or explicitly provided
	baseURL := opts.BaseURL
	token := opts.Token
	if baseURL == "" && info != nil {
		port := info.WeFlowPort
		if port == 0 {
			port = 5031
		}
		baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	}
	if token == "" && info != nil {
		token = info.WeFlowToken
	}

	c.baseURL = strings.TrimRight(baseURL, "/")
	c.token = token

	if c.baseURL != "" {
		_ = c.ensureRunning()
	}

	return c, nil
}

func (c *Client) ensureRunning() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if c.isHealthy(ctx) {
		return nil
	}

	appImage := findWeFlowAppImage()
	if appImage == "" {
		return errors.New("WeFlow HTTP API is not running on 127.0.0.1:5031 and WeFlow AppImage was not found")
	}

	fakeClock, err := wechat.EnsureFakeClock()
	if err != nil {
		return fmt.Errorf("failed to prepare fake clock: %w", err)
	}

	cmd := exec.Command(appImage, "--no-sandbox")
	cmd.Env = append(os.Environ(), "LD_PRELOAD="+fakeClock)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to launch WeFlow: %w", err)
	}

	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		checkCtx, checkCancel := context.WithTimeout(context.Background(), 1*time.Second)
		if c.isHealthy(checkCtx) {
			checkCancel()
			return nil
		}
		checkCancel()
	}

	return errors.New("timeout waiting for WeFlow HTTP API to become ready")
}

func (c *Client) isHealthy(ctx context.Context) bool {
	if c.baseURL == "" {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func findWeFlowAppImage() string {
	if custom := os.Getenv("WEFLOW_PATH"); custom != "" {
		if fi, err := os.Stat(custom); err == nil && (fi.Mode()&0111) != 0 {
			return custom
		}
	}
	if custom := os.Getenv("WEFLOW_APPIMAGE"); custom != "" {
		if fi, err := os.Stat(custom); err == nil && (fi.Mode()&0111) != 0 {
			return custom
		}
	}

	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, "Downloads", "WeFlow-6.3.1-Setup.appimage"),
		filepath.Join(home, "Downloads", "WeFlow-*-Setup.appimage"),
		filepath.Join(home, "Downloads", "WeFlow*.appimage"),
		filepath.Join(home, "Applications", "WeFlow*.appimage"),
		filepath.Join(home, ".local", "bin", "weflow"),
		filepath.Join(home, ".local", "share", "weflow", "weflow"),
		filepath.Join(home, "Workspace", "WeFlow", "release", "linux-unpacked", "weflow"),
		"/usr/local/bin/weflow",
		"/opt/weflow/weflow",
		"/usr/bin/weflow",
	}

	for _, pattern := range candidates {
		matches, err := filepath.Glob(pattern)
		if err == nil && len(matches) > 0 {
			for _, m := range matches {
				if fi, err := os.Stat(m); err == nil && (fi.Mode()&0111) != 0 {
					return m
				}
			}
		}
	}
	return ""
}

func (c *Client) get(ctx context.Context, path string, q url.Values) ([]byte, error) {
	reqURL := fmt.Sprintf("%s%s", c.baseURL, path)
	if len(q) > 0 {
		reqURL += "?" + q.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ensureErr := c.ensureRunning(); ensureErr == nil {
			reqRetry, retryErr := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
			if retryErr == nil {
				reqRetry.Header.Set("Authorization", "Bearer "+c.token)
				reqRetry.Header.Set("Accept", "application/json")
				resp, err = c.httpClient.Do(reqRetry)
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &errResp)
		if errResp.Error != "" {
			return nil, fmt.Errorf("WeFlow error (%d): %s", resp.StatusCode, errResp.Error)
		}
		return nil, fmt.Errorf("WeFlow error: status %d", resp.StatusCode)
	}

	return body, nil
}

// ListSessions queries sessions directly from native WCDB or through HTTP bridge fallback.
func (c *Client) ListSessions(ctx context.Context, keyword string, limit, offset int) (*SessionsResponse, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	if c.useNative {
		rawList, err := wcdb.GetSessions()
		if err != nil {
			return nil, err
		}

		var filtered []Session
		for _, raw := range rawList {
			username := fmt.Sprintf("%v", raw["username"])
			if username == "" || strings.HasPrefix(username, "@placeholder") {
				continue
			}

			disp := fmt.Sprintf("%v", raw["displayName"])
			if disp == "" || disp == "<nil>" {
				disp = fmt.Sprintf("%v", raw["last_sender_display_name"])
			}
			if disp == "" || disp == "<nil>" {
				// Lookup contact for real name
				if contact, err := wcdb.GetContact(username); err == nil && contact != nil {
					disp = fmt.Sprintf("%v", contact["remark"])
					if disp == "" || disp == "<nil>" {
						disp = fmt.Sprintf("%v", contact["nickName"])
					}
				}
			}
			if disp == "" || disp == "<nil>" {
				disp = username
			}

			if keyword != "" {
				if !strings.Contains(strings.ToLower(username), strings.ToLower(keyword)) &&
					!strings.Contains(strings.ToLower(disp), strings.ToLower(keyword)) {
					continue
				}
			}

			lastTs, _ := strconv.ParseInt(fmt.Sprintf("%v", raw["last_timestamp"]), 10, 64)
			unread, _ := strconv.Atoi(fmt.Sprintf("%v", raw["unread_count"]))
			sType, _ := strconv.Atoi(fmt.Sprintf("%v", raw["type"]))

			sessType := "single"
			if strings.HasSuffix(username, "@chatroom") {
				sessType = "group"
			} else if strings.HasSuffix(username, "@openim") {
				sessType = "openim"
			}

			if c.sanitize {
				disp = sanitize.Text(disp)
			}

			filtered = append(filtered, Session{
				SessionID:     username,
				Username:      username,
				DisplayName:   disp,
				Type:          sType,
				SessionType:   sessType,
				LastTimestamp: lastTs,
				UnreadCount:   unread,
			})
		}

		// Sort by last timestamp descending
		sort.Slice(filtered, func(i, j int) bool {
			return filtered[i].LastTimestamp > filtered[j].LastTimestamp
		})

		start := offset
		if start > len(filtered) {
			start = len(filtered)
		}
		end := start + limit
		if end > len(filtered) {
			end = len(filtered)
		}

		slice := filtered[start:end]
		hasMore := end < len(filtered)
		nextOffset := 0
		if hasMore {
			nextOffset = end
		}

		return &SessionsResponse{
			Sessions:   slice,
			HasMore:    hasMore,
			NextOffset: nextOffset,
		}, nil
	}

	// Fallback to HTTP
	q := url.Values{}
	q.Set("limit", strconv.Itoa(limit))
	q.Set("offset", strconv.Itoa(offset))
	if keyword != "" {
		q.Set("keyword", keyword)
	}

	body, err := c.get(ctx, "/api/v1/sessions", q)
	if err != nil {
		return nil, err
	}

	var raw struct {
		Success  bool `json:"success"`
		Sessions []struct {
			Username      string `json:"username"`
			DisplayName   string `json:"displayName"`
			Type          int    `json:"type"`
			SessionType   string `json:"sessionType"`
			LastTimestamp int64  `json:"lastTimestamp"`
			UnreadCount   int    `json:"unreadCount"`
		} `json:"sessions"`
		HasMore bool `json:"hasMore"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse sessions response: %w", err)
	}

	sessions := make([]Session, len(raw.Sessions))
	for i, s := range raw.Sessions {
		displayName := s.DisplayName
		if displayName == "" {
			displayName = s.Username
		}
		if c.sanitize {
			displayName = sanitize.Text(displayName)
		}
		sessions[i] = Session{
			SessionID:     s.Username,
			Username:      s.Username,
			DisplayName:   displayName,
			Type:          s.Type,
			SessionType:   s.SessionType,
			LastTimestamp: s.LastTimestamp,
			UnreadCount:   s.UnreadCount,
		}
	}

	hasMore := raw.HasMore
	if !hasMore && len(raw.Sessions) >= limit {
		hasMore = true
	}

	nextOffset := 0
	if hasMore {
		nextOffset = offset + len(raw.Sessions)
	}

	return &SessionsResponse{
		Sessions:   sessions,
		HasMore:    hasMore,
		NextOffset: nextOffset,
	}, nil
}

// ListMessages queries messages in a chat session.
func (c *Client) ListMessages(ctx context.Context, sessionID string, limit, offset int, order string, since, end int64) (*MessagesResponse, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	if c.useNative {
		rawMsgs, err := wcdb.GetMessages(sessionID, limit, offset)
		if err != nil {
			return nil, err
		}

		var msgs []Message
		for _, m := range rawMsgs {
			createTime, _ := strconv.ParseInt(fmt.Sprintf("%v", m["create_time"]), 10, 64)
			if since > 0 && createTime < since {
				continue
			}
			if end > 0 && createTime > end {
				continue
			}

			idStr := fmt.Sprintf("%v", m["server_id"])
			if idStr == "" || idStr == "0" || idStr == "<nil>" {
				idStr = fmt.Sprintf("%v", m["local_id"])
			}

			sender := fmt.Sprintf("%v", m["sender_username"])
			content := fmt.Sprintf("%v", m["message_content"])
			lType, _ := strconv.Atoi(fmt.Sprintf("%v", m["local_type"]))

			// For chatrooms, content often has format "wxid:\nactual_content"
			if strings.HasSuffix(sessionID, "@chatroom") && strings.Contains(content, ":\n") {
				parts := strings.SplitN(content, ":\n", 2)
				if sender == "" || sender == "<nil>" {
					sender = parts[0]
				}
				content = parts[1]
			}

			if c.sanitize {
				content = sanitize.Text(content)
			}

			fName, fPath, fSize := extractFileAttachment(content, c.accountDir)

			msgs = append(msgs, Message{
				ID:        idStr,
				SessionID: sessionID,
				Sender:    sender,
				Timestamp: createTime,
				Type:      lType,
				Content:   content,
				FileName:  fName,
				FilePath:  fPath,
				FileSize:  fSize,
			})
		}

		if order == "desc" {
			sort.Slice(msgs, func(i, j int) bool {
				return msgs[i].Timestamp > msgs[j].Timestamp
			})
		} else {
			sort.Slice(msgs, func(i, j int) bool {
				return msgs[i].Timestamp < msgs[j].Timestamp
			})
		}

		hasMore := len(rawMsgs) >= limit
		nextOffset := 0
		if hasMore {
			nextOffset = offset + len(msgs)
		}

		return &MessagesResponse{
			Messages:   msgs,
			HasMore:    hasMore,
			NextOffset: nextOffset,
		}, nil
	}

	// Fallback to HTTP
	q := url.Values{}
	q.Set("limit", strconv.Itoa(limit))
	q.Set("offset", strconv.Itoa(offset))
	q.Set("media", "0")
	if since > 0 {
		q.Set("start", strconv.FormatInt(since, 10))
	}
	if end > 0 {
		q.Set("end", strconv.FormatInt(end, 10))
	}

	var rawPath string
	if order == "desc" || order == "" {
		q.Set("talker", sessionID)
		rawPath = "/api/v1/messages"
	} else {
		rawPath = fmt.Sprintf("/api/v1/sessions/%s/messages", url.PathEscape(sessionID))
		if since > 0 {
			q.Set("since", strconv.FormatInt(since, 10))
		}
	}

	body, err := c.get(ctx, rawPath, q)
	if err != nil {
		return nil, err
	}

	return c.parseMessages(body, sessionID, limit, offset)
}

// SearchMessages searches messages by keyword across all sessions or in a specific session.
func (c *Client) SearchMessages(ctx context.Context, keyword, sessionID string, limit, offset int, since, end int64) (*MessagesResponse, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	if c.useNative {
		rawMsgs, err := wcdb.SearchMessages(sessionID, keyword, limit, offset, int(since), int(end))
		if err != nil {
			return nil, err
		}

		var msgs []Message
		for _, m := range rawMsgs {
			createTime, _ := strconv.ParseInt(fmt.Sprintf("%v", m["create_time"]), 10, 64)
			idStr := fmt.Sprintf("%v", m["local_id"])
			sender := fmt.Sprintf("%v", m["sender_username"])
			content := fmt.Sprintf("%v", m["content"])
			lType, _ := strconv.Atoi(fmt.Sprintf("%v", m["local_type"]))
			sess := fmt.Sprintf("%v", m["_session_id"])
			if sess == "" || sess == "<nil>" {
				sess = sessionID
			}

			if c.sanitize {
				content = sanitize.Text(content)
			}

			msgs = append(msgs, Message{
				ID:        idStr,
				SessionID: sess,
				Sender:    sender,
				Timestamp: createTime,
				Type:      lType,
				Content:   content,
			})
		}

		hasMore := len(rawMsgs) >= limit
		nextOffset := 0
		if hasMore {
			nextOffset = offset + len(msgs)
		}

		return &MessagesResponse{
			Messages:   msgs,
			HasMore:    hasMore,
			NextOffset: nextOffset,
		}, nil
	}

	// Fallback to HTTP
	q := url.Values{}
	q.Set("keyword", keyword)
	q.Set("limit", strconv.Itoa(limit))
	q.Set("offset", strconv.Itoa(offset))
	q.Set("media", "0")
	if sessionID != "" {
		q.Set("talker", sessionID)
	}
	if since > 0 {
		q.Set("start", strconv.FormatInt(since, 10))
	}
	if end > 0 {
		q.Set("end", strconv.FormatInt(end, 10))
	}

	body, err := c.get(ctx, "/api/v1/messages", q)
	if err != nil {
		return nil, err
	}

	return c.parseMessages(body, sessionID, limit, offset)
}

func (c *Client) parseMessages(body []byte, defaultSessionID string, limit, offset int) (*MessagesResponse, error) {
	var raw struct {
		Talker   string `json:"talker"`
		Messages []struct {
			LocalID        interface{} `json:"localId"`
			ServerID       interface{} `json:"serverId"`
			LocalType      int         `json:"localType"`
			CreateTime     int64       `json:"createTime"`
			Timestamp      int64       `json:"timestamp"`
			SenderUsername string      `json:"senderUsername"`
			Sender         string      `json:"sender"`
			Content        string      `json:"content"`
			Talker         string      `json:"talker"`
		} `json:"messages"`
		HasMore bool `json:"hasMore"`
		Sync    *struct {
			HasMore    bool  `json:"hasMore"`
			NextOffset int   `json:"nextOffset"`
			NextSince  int64 `json:"nextSince"`
		} `json:"sync"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse messages response: %w", err)
	}

	msgs := make([]Message, len(raw.Messages))
	for i, m := range raw.Messages {
		idStr := ""
		if m.ServerID != nil && fmt.Sprintf("%v", m.ServerID) != "" && fmt.Sprintf("%v", m.ServerID) != "0" {
			idStr = fmt.Sprintf("%v", m.ServerID)
		} else if m.LocalID != nil {
			idStr = fmt.Sprintf("%v", m.LocalID)
		}

		sess := m.Talker
		if sess == "" {
			sess = raw.Talker
		}
		if sess == "" {
			sess = defaultSessionID
		}

		sender := m.SenderUsername
		if sender == "" {
			sender = m.Sender
		}

		ts := m.CreateTime
		if ts == 0 {
			ts = m.Timestamp
		}

		content := m.Content
		if c.sanitize {
			content = sanitize.Text(content)
		}

		msgs[i] = Message{
			ID:        idStr,
			SessionID: sess,
			Sender:    sender,
			Timestamp: ts,
			Type:      m.LocalType,
			Content:   content,
		}
	}

	hasMore := raw.HasMore
	if raw.Sync != nil {
		hasMore = raw.Sync.HasMore
	} else if !hasMore && len(raw.Messages) >= limit {
		hasMore = true
	}

	nextOffset := 0
	if hasMore {
		if raw.Sync != nil && raw.Sync.NextOffset > 0 {
			nextOffset = raw.Sync.NextOffset
		} else {
			nextOffset = offset + len(raw.Messages)
		}
	}

	return &MessagesResponse{
		Messages:   msgs,
		HasMore:    hasMore,
		NextOffset: nextOffset,
	}, nil
}

// GetContact queries contact info by exact username.
func (c *Client) GetContact(ctx context.Context, username string) (*Contact, error) {
	if c.useNative {
		m, err := wcdb.GetContact(username)
		if err != nil {
			return nil, err
		}
		disp := fmt.Sprintf("%v", m["remark"])
		if disp == "" || disp == "<nil>" {
			disp = fmt.Sprintf("%v", m["nickName"])
		}
		if disp == "" || disp == "<nil>" {
			disp = username
		}

		nick := fmt.Sprintf("%v", m["nickName"])
		remark := fmt.Sprintf("%v", m["remark"])
		if nick == "<nil>" {
			nick = ""
		}
		if remark == "<nil>" {
			remark = ""
		}

		if c.sanitize {
			disp = sanitize.Text(disp)
			nick = sanitize.Text(nick)
			remark = sanitize.Text(remark)
		}

		sType := "contact"
		if strings.HasSuffix(username, "@chatroom") {
			sType = "group"
		}

		return &Contact{
			Username:    username,
			Nickname:    nick,
			Remark:      remark,
			DisplayName: disp,
			Type:        sType,
		}, nil
	}

	// Fallback to HTTP
	q := url.Values{}
	q.Set("keyword", username)
	q.Set("limit", "1000")

	body, err := c.get(ctx, "/api/v1/contacts", q)
	if err != nil {
		return nil, err
	}

	var raw struct {
		Count    int `json:"count"`
		Contacts []struct {
			Username    string `json:"username"`
			DisplayName string `json:"displayName"`
			Remark      string `json:"remark"`
			Nickname    string `json:"nickname"`
			Type        string `json:"type"`
		} `json:"contacts"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse contacts response: %w", err)
	}

	for _, item := range raw.Contacts {
		if item.Username == username {
			disp := item.DisplayName
			if disp == "" {
				disp = item.Remark
			}
			if disp == "" {
				disp = item.Nickname
			}
			if disp == "" {
				disp = item.Username
			}

			nick := item.Nickname
			remark := item.Remark
			if c.sanitize {
				disp = sanitize.Text(disp)
				nick = sanitize.Text(nick)
				remark = sanitize.Text(remark)
			}

			return &Contact{
				Username:    item.Username,
				Nickname:    nick,
				Remark:      remark,
				DisplayName: disp,
				Type:        item.Type,
			}, nil
		}
	}

	return nil, fmt.Errorf("contact %s not found", username)
}

// ListFiles lists received/downloaded files from WeChat msg/file directory.
func (c *Client) ListFiles(ctx context.Context, keyword, ext string, limit, offset int) ([]files.FileInfo, bool, error) {
	if c.accountDir == "" {
		info, err := wechat.DetectAccount()
		if err != nil {
			return nil, false, err
		}
		c.accountDir = info.AccountDir
	}
	return files.ListFiles(c.accountDir, keyword, ext, limit, offset)
}

// GetFile locates a specific file by name and returns its local details.
func (c *Client) GetFile(ctx context.Context, fileName string) (*files.FileInfo, error) {
	if c.accountDir == "" {
		info, err := wechat.DetectAccount()
		if err != nil {
			return nil, err
		}
		c.accountDir = info.AccountDir
	}
	return files.GetFile(c.accountDir, fileName)
}

func extractFileAttachment(content, accountDir string) (string, string, int64) {
	if accountDir == "" || !strings.Contains(content, "<title>") {
		return "", "", 0
	}
	start := strings.Index(content, "<title>")
	if start == -1 {
		return "", "", 0
	}
	start += len("<title>")
	end := strings.Index(content[start:], "</title>")
	if end == -1 {
		return "", "", 0
	}
	title := strings.TrimSpace(content[start : start+end])
	title = strings.TrimPrefix(title, "<![CDATA[")
	title = strings.TrimSuffix(title, "]]>")
	title = strings.TrimSpace(title)

	if title == "" || !strings.Contains(title, ".") {
		return "", "", 0
	}

	if f, err := files.GetFile(accountDir, title); err == nil && f != nil {
		return f.FileName, f.FilePath, f.Size
	}
	return title, "", 0
}
