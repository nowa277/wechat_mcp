package bridge

import (
	"context"
	"testing"
)

func TestBridgeLive(t *testing.T) {
	client, err := NewClient(ClientOptions{
		BaseURL: "http://127.0.0.1:5031",
		Token:   "wechat_mcp_local_token_8888",
	})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	ctx := context.Background()
	sessions, err := client.ListSessions(ctx, "", 5, 0)
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	if len(sessions.Sessions) == 0 {
		t.Fatalf("Expected sessions > 0")
	}

	t.Logf("Got %d sessions. First session: %+v", len(sessions.Sessions), sessions.Sessions[0])

	// Read messages of first session
	firstSessID := sessions.Sessions[0].SessionID
	msgs, err := client.ListMessages(ctx, firstSessID, 3, 0, "desc", 0, 0)
	if err != nil {
		t.Fatalf("ListMessages failed: %v", err)
	}
	t.Logf("Got %d messages from %s. First msg: %+v", len(msgs.Messages), firstSessID, msgs.Messages[0])

	// Get contact of first session
	contact, err := client.GetContact(ctx, firstSessID)
	if err != nil {
		t.Logf("GetContact info: %v", err)
	} else {
		t.Logf("Contact: %+v", contact)
	}
}
