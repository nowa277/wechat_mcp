package sanitize

import (
	"testing"
)

func TestTextSanitize(t *testing.T) {
	input := "请联系 test@example.com 或拨打 13800138000，身份证是 110101199003072345，卡号 6222020200112233445"
	expected := "请联系 [EMAIL] 或拨打 [PHONE]，身份证是 [ID_CARD]，卡号 [BANK_CARD]"
	res := Text(input)
	if res != expected {
		t.Errorf("Mismatch:\nGot:  %s\nWant: %s", res, expected)
	}
}

func TestPayloadSanitize(t *testing.T) {
	data := map[string]interface{}{
		"id":        "13800138000", // should be protected
		"content":   "我的电话是 13912345678",
		"sessionId": "wxid_123@chatroom",
	}
	res := Payload(data).(map[string]interface{})
	if res["id"] != "13800138000" {
		t.Errorf("ID was mutated: %v", res["id"])
	}
	if res["content"] != "我的电话是 [PHONE]" {
		t.Errorf("Content was not sanitized: %v", res["content"])
	}
}
