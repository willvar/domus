package handler

import (
	"os"
	"path/filepath"
	"strings"
)

var textExtensions = map[string]bool{
	".txt": true, ".md": true, ".json": true, ".yaml": true, ".yml": true,
	".toml": true, ".xml": true, ".csv": true, ".log": true, ".conf": true,
	".ini": true, ".sh": true, ".bash": true, ".zsh": true, ".fish": true,
	".go": true, ".py": true, ".js": true, ".ts": true, ".jsx": true, ".tsx": true,
	".vue": true, ".html": true, ".htm": true, ".css": true, ".scss": true, ".less": true,
	".sql": true, ".rs": true, ".c": true, ".cpp": true, ".h": true, ".hpp": true,
	".java": true, ".kt": true, ".rb": true, ".php": true, ".pl": true, ".lua": true,
	".r": true, ".swift": true, ".dart": true, ".ex": true, ".exs": true,
	".env": true, ".gitignore": true, ".dockerignore": true, ".editorconfig": true,
	".makefile": true, ".cmake": true, ".gradle": true, ".properties": true,
}

// isTextFile returns true if the file extension indicates a text/code file.
func isTextFile(fileName string) bool {
	ext := strings.ToLower(filepath.Ext(fileName))
	return textExtensions[ext]
}

// indexFile updates the search vector for a file. If indexContent is true and the file
// is a text file, it reads the plaintext content from localFile and indexes name + content.
// Otherwise it indexes only the file name.
func (h *Handler) indexFile(userID, ossKey, fileName, localFile string, indexContent bool) {
	text := fileName
	if indexContent && isTextFile(fileName) && localFile != "" {
		if content, err := os.ReadFile(localFile); err == nil {
			text = fileName + "\n" + string(content)
		}
	}
	_ = h.Repos.Files.UpdateSearchVector(userID, ossKey, text)
}

// indexFileName updates the search vector with only the file name.
func (h *Handler) indexFileName(userID, ossKey, fileName string) {
	_ = h.Repos.Files.UpdateSearchVector(userID, ossKey, fileName)
}
