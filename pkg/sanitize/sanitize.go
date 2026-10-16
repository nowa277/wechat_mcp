package sanitize

import (
	"regexp"
	"strings"
)

var (
	emailRegex    = regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`)
	idCardRegex   = regexp.MustCompile(`\b(?:[1-9]\d{5}(?:18|19|20)\d{2}(?:0[1-9]|1[0-2])(?:0[1-9]|[12]\d|3[01])\d{3}[\dXx]|\d{17}[Xx])\b`)
	bankCardRegex = regexp.MustCompile(`\b(?:\d{4}[ -]){3,4}\d{1,4}\b|\b\d{16,19}\b`)
	phoneRegex    = regexp.MustCompile(`(?:\+?86[- ]?)?\b1[3-9]\d[- ]?\d{4}[- ]?\d{4}\b|\b0\d{2,3}[- ]\d{7,8}\b`)
)

var protectedKeys = map[string]bool{
	"id":                true,
	"localid":           true,
	"serverid":          true,
	"sessionid":         true,
	"platformmessageid": true,
	"username":          true,
	"sender":            true,
	"senderusername":    true,
	"talker":            true,
	"type":              true,
	"timestamp":         true,
	"createtime":        true,
	"sortseq":           true,
	"lasttimestamp":     true,
	"unreadcount":       true,
	"hasmore":           true,
	"nextoffset":        true,
}

// Text masks PII in text strings.
func Text(s string) string {
	if s == "" {
		return s
	}
	s = emailRegex.ReplaceAllString(s, "[EMAIL]")
	s = idCardRegex.ReplaceAllString(s, "[ID_CARD]")
	s = bankCardRegex.ReplaceAllString(s, "[BANK_CARD]")
	s = phoneRegex.ReplaceAllString(s, "[PHONE]")
	return s
}

// Payload recursively masks PII in a generic JSON structure while preserving IDs.
func Payload(v interface{}) interface{} {
	switch val := v.(type) {
	case string:
		return Text(val)
	case map[string]interface{}:
		out := make(map[string]interface{}, len(val))
		for k, item := range val {
			if protectedKeys[strings.ToLower(k)] {
				out[k] = item
			} else {
				out[k] = Payload(item)
			}
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(val))
		for i, item := range val {
			out[i] = Payload(item)
		}
		return out
	default:
		return v
	}
}
