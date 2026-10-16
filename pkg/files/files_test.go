package files

import (
	"testing"

	"github.com/nowa277/wechat_mcp/pkg/wechat"
)

func TestFiles(t *testing.T) {
	info, err := wechat.DetectAccount()
	if err != nil || info == nil || info.AccountDir == "" {
		t.Skip("WeChat account directory not found on this machine, skipping test")
	}

	files, hasMore, err := ListFiles(info.AccountDir, "", "", 10, 0)
	if err != nil {
		t.Fatalf("ListFiles failed: %v", err)
	}
	t.Logf("ListFiles returned %d files (hasMore: %v)", len(files), hasMore)
	if len(files) == 0 {
		t.Skip("No files found in msg/file directory, skipping")
	}

	first := files[0]
	t.Logf("First file: %s (%d bytes, path: %s)", first.FileName, first.Size, first.FilePath)

	// Test GetFile
	file, err := GetFile(info.AccountDir, first.FileName)
	if err != nil {
		t.Fatalf("GetFile failed: %v", err)
	}
	t.Logf("GetFile verified: %s -> %s", file.FileName, file.FilePath)
}
