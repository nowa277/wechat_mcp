package wechat

import (
	"testing"
)

func TestDetectAccount(t *testing.T) {
	info, err := DetectAccount()
	if err != nil {
		t.Fatalf("DetectAccount failed: %v", err)
	}
	if info.WxID == "" {
		t.Errorf("Expected WxID to be detected, got empty")
	}
	t.Logf("Detected: WxID=%s, Nickname=%s, DataDir=%s, AccountDir=%s, WeFlowPort=%d",
		info.WxID, info.Nickname, info.DataDir, info.AccountDir, info.WeFlowPort)
}

func TestEnsureFakeClock(t *testing.T) {
	p, err := EnsureFakeClock()
	if err != nil {
		t.Fatalf("EnsureFakeClock failed: %v", err)
	}
	if p == "" {
		t.Errorf("Expected path, got empty")
	}
}
