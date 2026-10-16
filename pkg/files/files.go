package files

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type FileInfo struct {
	FileName string `json:"fileName"`
	FilePath string `json:"filePath"`
	Size     int64  `json:"size"`
	ModTime  int64  `json:"modTime"`
	Ext      string `json:"ext"`
	Month    string `json:"month,omitempty"`
}

// ListFiles scans AccountDir/msg/file for native unencrypted WeChat attachments.
func ListFiles(accountDir, keyword, ext string, limit, offset int) ([]FileInfo, bool, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	fileRoot := filepath.Join(accountDir, "msg", "file")
	if fi, err := os.Stat(fileRoot); err != nil || !fi.IsDir() {
		return nil, false, fmt.Errorf("file directory not found: %s", fileRoot)
	}

	monthDirs, err := os.ReadDir(fileRoot)
	if err != nil {
		return nil, false, err
	}

	var allFiles []FileInfo
	ext = strings.ToLower(strings.TrimPrefix(ext, "."))

	for _, md := range monthDirs {
		if !md.IsDir() {
			continue
		}
		monthPath := filepath.Join(fileRoot, md.Name())
		entries, err := os.ReadDir(monthPath)
		if err != nil {
			continue
		}

		for _, e := range entries {
			if e.IsDir() {
				continue
			}

			name := e.Name()
			fileExt := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))

			if ext != "" && fileExt != ext {
				continue
			}

			if keyword != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(keyword)) {
				continue
			}

			info, err := e.Info()
			if err != nil {
				continue
			}

			allFiles = append(allFiles, FileInfo{
				FileName: name,
				FilePath: filepath.Join(monthPath, name),
				Size:     info.Size(),
				ModTime:  info.ModTime().Unix(),
				Ext:      fileExt,
				Month:    md.Name(),
			})
		}
	}

	// Sort by modification time descending
	sort.Slice(allFiles, func(i, j int) bool {
		return allFiles[i].ModTime > allFiles[j].ModTime
	})

	start := offset
	if start > len(allFiles) {
		start = len(allFiles)
	}
	end := start + limit
	if end > len(allFiles) {
		end = len(allFiles)
	}

	hasMore := end < len(allFiles)
	return allFiles[start:end], hasMore, nil
}

// GetFile locates a specific file by exact or partial name.
func GetFile(accountDir, fileName string) (*FileInfo, error) {
	fileRoot := filepath.Join(accountDir, "msg", "file")
	if fi, err := os.Stat(fileRoot); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("file directory not found: %s", fileRoot)
	}

	monthDirs, err := os.ReadDir(fileRoot)
	if err != nil {
		return nil, err
	}

	cleanTarget := strings.ToLower(strings.TrimSpace(fileName))

	var fallbackMatch *FileInfo

	// Search newest months first
	sort.Slice(monthDirs, func(i, j int) bool {
		return monthDirs[i].Name() > monthDirs[j].Name()
	})

	for _, md := range monthDirs {
		if !md.IsDir() {
			continue
		}
		monthPath := filepath.Join(fileRoot, md.Name())
		entries, err := os.ReadDir(monthPath)
		if err != nil {
			continue
		}

		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			lowerName := strings.ToLower(name)

			if lowerName == cleanTarget {
				info, _ := e.Info()
				return &FileInfo{
					FileName: name,
					FilePath: filepath.Join(monthPath, name),
					Size:     info.Size(),
					ModTime:  info.ModTime().Unix(),
					Ext:      strings.ToLower(strings.TrimPrefix(filepath.Ext(name), ".")),
					Month:    md.Name(),
				}, nil
			}

			if fallbackMatch == nil && strings.Contains(lowerName, cleanTarget) {
				info, _ := e.Info()
				fallbackMatch = &FileInfo{
					FileName: name,
					FilePath: filepath.Join(monthPath, name),
					Size:     info.Size(),
					ModTime:  info.ModTime().Unix(),
					Ext:      strings.ToLower(strings.TrimPrefix(filepath.Ext(name), ".")),
					Month:    md.Name(),
				}
			}
		}
	}

	if fallbackMatch != nil {
		return fallbackMatch, nil
	}

	return nil, fmt.Errorf("file not found: %s", fileName)
}
