package wcdb

import (
	"testing"

	"github.com/nowa277/wechat_mcp/pkg/wechat"
)

func TestNativeWCDBDirect(t *testing.T) {
	info, err := wechat.DetectAccount()
	if err != nil || info == nil || info.SessionDb == "" || info.DecryptKey == "" {
		t.Skip("WeChat account or decrypt key not configured on this machine, skipping live db test")
	}

	handle, err := OpenAccount(info.SessionDb, info.DecryptKey)
	if err != nil {
		t.Fatalf("OpenAccount failed: %v", err)
	}
	t.Logf("Successfully opened account! Handle: %d", handle)

	sessions, err := GetSessions()
	if err != nil {
		t.Fatalf("GetSessions failed: %v", err)
	}
	t.Logf("Got %d sessions directly via CGO!", len(sessions))
	if len(sessions) == 0 {
		t.Fatalf("Expected sessions > 0")
	}

	first := sessions[0]
	t.Logf("First session: %v (%v)", first["username"], first["displayName"])

	if firstUsername, ok := first["username"].(string); ok && firstUsername != "" {
		msgs, err := GetMessages(firstUsername, 3, 0)
		if err != nil {
			t.Logf("GetMessages notice: %v", err)
		} else {
			t.Logf("Got %d messages!", len(msgs))
		}

		contact, err := GetContact(firstUsername)
		if err != nil {
			t.Logf("GetContact info: %v", err)
		} else {
			t.Logf("Contact: %v (%v)", contact["username"], contact["displayName"])
		}
	}
}
