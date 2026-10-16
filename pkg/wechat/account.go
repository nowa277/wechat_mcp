package wechat

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// AccountInfo holds detected WeChat account information.
type AccountInfo struct {
	WxID        string `json:"wxid"`
	Nickname    string `json:"nickname"`
	DataDir     string `json:"dataDir"`
	AccountDir  string `json:"accountDir"`
	StorageDir  string `json:"storageDir"`
	SessionDb   string `json:"sessionDb,omitempty"`
	DecryptKey  string `json:"decryptKey,omitempty"`
	WeFlowToken string `json:"weflowToken,omitempty"`
	WeFlowPort  int    `json:"weflowPort,omitempty"`
}

// CryptKey is the fixed WeChat Linux global_config AES key.
var CryptKey = []byte("xwechat_crypt_key")

// DetectAccount discovers local WeChat account information automatically.
func DetectAccount() (*AccountInfo, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get user home dir: %w", err)
	}

	dataDirs := []string{
		filepath.Join(home, "Documents", "xwechat_files"),
		filepath.Join(home, ".xwechat", "xwechat_files"),
	}

	var foundDataDir string
	for _, d := range dataDirs {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			foundDataDir = d
			break
		}
	}
	if foundDataDir == "" {
		return nil, errors.New("wechat data directory not found (checked ~/Documents/xwechat_files and ~/.xwechat/xwechat_files)")
	}

	info := &AccountInfo{DataDir: foundDataDir}

	// Try reading and decrypting all_users/config/global_config
	globalConfig := filepath.Join(foundDataDir, "all_users", "config", "global_config")
	if data, err := os.ReadFile(globalConfig); err == nil && len(data) > 4 {
		block, err := aes.NewCipher(CryptKey[:16])
		if err == nil {
			iv := make([]byte, 16)
			stream := cipher.NewCFBDecrypter(block, iv)
			decrypted := make([]byte, len(data)-4)
			stream.XORKeyStream(decrypted, data[4:])

			info.WxID = extractMmkvString(decrypted, "mmkv_key_user_name")
			info.Nickname = extractMmkvString(decrypted, "mmkv_key_nick_name")
		}
	}

	// Find user directory under dataDir
	entries, err := os.ReadDir(foundDataDir)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			if strings.HasPrefix(name, "wxid_") || (info.WxID != "" && strings.HasPrefix(name, info.WxID)) {
				storagePath := filepath.Join(foundDataDir, name, "db_storage")
				if fi, err := os.Stat(storagePath); err == nil && fi.IsDir() {
					info.AccountDir = filepath.Join(foundDataDir, name)
					info.StorageDir = storagePath
					if info.WxID == "" {
						info.WxID = name
					}
					candSession := filepath.Join(storagePath, "session", "session.db")
					if fi, err := os.Stat(candSession); err == nil && !fi.IsDir() {
						info.SessionDb = candSession
					} else {
						candDirect := filepath.Join(storagePath, "session.db")
						if fi, err := os.Stat(candDirect); err == nil && !fi.IsDir() {
							info.SessionDb = candDirect
						}
					}
					break
				}
			}
		}
	}

	// Discover DecryptKey
	if envKey := os.Getenv("WECHAT_DB_KEY"); envKey != "" {
		info.DecryptKey = envKey
	}
	if info.DecryptKey == "" {
		mcpCfgPath := filepath.Join(home, ".config", "wechat_mcp", "config.json")
		if mcpData, err := os.ReadFile(mcpCfgPath); err == nil {
			var mcpCfg map[string]interface{}
			if err := json.Unmarshal(mcpData, &mcpCfg); err == nil {
				if k, ok := mcpCfg["decryptKey"].(string); ok && len(k) == 64 {
					info.DecryptKey = k
				}
			}
		}
	}

	// Read or auto-configure WeFlow config
	weflowDir := filepath.Join(home, ".config", "weflow")
	weflowCfgPath := filepath.Join(weflowDir, "WeFlow-config.json")
	if cfgData, err := os.ReadFile(weflowCfgPath); err == nil {
		var cfg map[string]interface{}
		if err := json.Unmarshal(cfgData, &cfg); err == nil {
			port := 5031
			if p, ok := cfg["httpApiPort"].(float64); ok && p > 0 {
				port = int(p)
			}
			token, _ := cfg["httpApiToken"].(string)

			// If token is empty or httpApiEnabled is false, auto-enable and write back
			enabled, _ := cfg["httpApiEnabled"].(bool)
			needsUpdate := false
			if !enabled {
				cfg["httpApiEnabled"] = true
				needsUpdate = true
			}
			if token == "" {
				randomBytes := make([]byte, 16)
				rand.Read(randomBytes)
				token = hex.EncodeToString(randomBytes)
				cfg["httpApiToken"] = token
				needsUpdate = true
			}
			if needsUpdate {
				if updatedJson, err := json.MarshalIndent(cfg, "", "  "); err == nil {
					_ = os.WriteFile(weflowCfgPath, updatedJson, 0644)
				}
			}

			info.WeFlowPort = port
			info.WeFlowToken = token
		}
	} else if os.IsNotExist(err) {
		_ = os.MkdirAll(weflowDir, 0755)
		randomBytes := make([]byte, 16)
		rand.Read(randomBytes)
		token := hex.EncodeToString(randomBytes)
		cfg := map[string]interface{}{
			"dbPath":         info.DataDir,
			"myWxid":         info.WxID,
			"onboardingDone": true,
			"httpApiEnabled": true,
			"httpApiPort":    5031,
			"httpApiToken":   token,
			"httpApiHost":    "127.0.0.1",
			"theme":          "dark",
			"language":       "zh-CN",
			"logEnabled":     false,
		}
		if b, err := json.MarshalIndent(cfg, "", "  "); err == nil {
			_ = os.WriteFile(weflowCfgPath, b, 0644)
		}
		info.WeFlowPort = 5031
		info.WeFlowToken = token
	}

	return info, nil
}

func extractMmkvString(buf []byte, keyName string) string {
	idx := strings.Index(string(buf), keyName)
	if idx == -1 {
		return ""
	}
	offset := idx + len(keyName)
	if offset >= len(buf) {
		return ""
	}
	// Read varints
	v1Val, v1Len := readVarint(buf[offset:])
	offset += v1Len
	if offset >= len(buf) {
		return ""
	}
	v2Val, v2Len := readVarint(buf[offset:])
	offset += v2Len
	_ = v1Val

	if v2Val > 0 && v2Val <= 10000 && offset+v2Val <= len(buf) {
		return strings.TrimSpace(string(buf[offset : offset+v2Val]))
	}
	return ""
}

func readVarint(buf []byte) (int, int) {
	var value int
	var shift uint
	for i, b := range buf {
		if i >= 4 {
			return value, i
		}
		value |= int(b&0x7f) << shift
		if (b & 0x80) == 0 {
			return value, i + 1
		}
		shift += 7
	}
	return value, len(buf)
}

// EnsureFakeClock creates or verifies /tmp/libfake_clock.so for WeFlow time-interception.
func EnsureFakeClock() (string, error) {
	libPath := "/tmp/libfake_clock.so"
	if fi, err := os.Stat(libPath); err == nil && fi.Size() > 0 {
		return libPath, nil
	}

	cSrc := `
#define _GNU_SOURCE
#include <time.h>
#include <dlfcn.h>

static int (*real_clock_gettime)(clockid_t clk_id, struct timespec *tp) = NULL;

int clock_gettime(clockid_t clk_id, struct timespec *tp) {
    if (!real_clock_gettime) {
        real_clock_gettime = dlsym(RTLD_NEXT, "clock_gettime");
    }
    int res = real_clock_gettime(clk_id, tp);
    if (res == 0 && clk_id == CLOCK_REALTIME) {
        // Shift time back by ~26 days (2246400s) smoothly into Sept 2026 to satisfy both -101 (expiration) and -102 (rollback check)
        tp->tv_sec -= 2246400;
    }
    return res;
}
`
	srcFile := "/tmp/fake_clock.c"
	if err := os.WriteFile(srcFile, []byte(cSrc), 0644); err != nil {
		return "", err
	}
	cmd := exec.Command("gcc", "-shared", "-fPIC", "-O2", srcFile, "-o", libPath, "-ldl")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("failed to compile fake_clock: %w, output: %s", err, string(out))
	}
	return libPath, nil
}
