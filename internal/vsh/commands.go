package vsh

// Command registry, constants, help, and shared helpers.

import (
	"fmt"
	"mime"
	"path"
	"path/filepath"
	"strings"
)

// cmdFunc is the signature for a virtual shell command.
type cmdFunc func(s *Session, args []string, redirect string) (string, error)

// commands is the registry of all available commands.
// Populated in init() to avoid initialization cycle (cmdWhich references commands).
var commands map[string]cmdFunc

func init() {
	commands = map[string]cmdFunc{
		// File system (cmd_fs.go)
		"ls": cmdLs, "cd": cmdCd, "pwd": cmdPwd,
		"mkdir": cmdMkdir, "touch": cmdTouch,
		"rm": cmdRm, "cp": cmdCp, "mv": cmdMv,
		"tree": cmdTree, "find": cmdFind,
		// Text processing (cmd_text.go)
		"cat": cmdCat, "head": cmdHead, "tail": cmdTail,
		"echo": cmdEcho, "grep": cmdGrep,
		"sort": cmdSort, "uniq": cmdUniq, "diff": cmdDiff, "wc": cmdWc,
		// Info & utility (cmd_info.go)
		"stat": cmdStat, "du": cmdDu, "file": cmdFile,
		"basename": cmdBasename, "dirname": cmdDirname,
		"which": cmdWhich, "type": cmdWhich,
		"md5sum": cmdMd5sum, "sha256sum": cmdSha256sum,
		"whoami": cmdWhoami, "date": cmdDate, "history": cmdHistory,
		"uname": cmdUname, "fastfetch": cmdFastfetch, "neofetch": cmdFastfetch,
		// Shell built-ins
		"clear": cmdClear, "help": cmdHelp, "exit": cmdExit,
		// ssh is handled specially in execVsh but listed here for tab completion
		"ssh": nil,
	}
}

// ANSI color helpers
const (
	colorReset  = "\033[0m"
	colorBlue   = "\033[1;34m"
	colorGreen  = "\033[1;32m"
	colorRed    = "\033[31m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
	colorDim    = "\033[2m"
)

// --- clear ---

func cmdClear(s *Session, args []string, redirect string) (string, error) {
	return "\033[2J\033[H", nil
}

// --- help ---

func cmdHelp(s *Session, args []string, redirect string) (string, error) {
	return colorCyan + "Zephyr Virtual Shell" + colorReset + "\r\n\r\n" +
		"Available commands:\r\n" +
		"  " + colorGreen + "ls" + colorReset + " [-l] [-a] [path]        List directory contents\r\n" +
		"  " + colorGreen + "cd" + colorReset + " [path]                  Change directory\r\n" +
		"  " + colorGreen + "pwd" + colorReset + "                        Print working directory\r\n" +
		"  " + colorGreen + "cat" + colorReset + " <file>                 Display file contents\r\n" +
		"  " + colorGreen + "head" + colorReset + " [-n N] <file>         Display first N lines\r\n" +
		"  " + colorGreen + "tail" + colorReset + " [-n N] <file>         Display last N lines\r\n" +
		"  " + colorGreen + "mkdir" + colorReset + " [-p] <dir>           Create directories\r\n" +
		"  " + colorGreen + "touch" + colorReset + " <file>               Create empty file\r\n" +
		"  " + colorGreen + "rm" + colorReset + " [-r] <path>             Remove files (to trash)\r\n" +
		"  " + colorGreen + "cp" + colorReset + " [-r] <src> <dst>        Copy files\r\n" +
		"  " + colorGreen + "mv" + colorReset + " <src> <dst>             Move/rename files\r\n" +
		"  " + colorGreen + "echo" + colorReset + " <text> [>/>> file]    Print text or write to file\r\n" +
		"  " + colorGreen + "find" + colorReset + " [path] -name <pat>    Search files by name\r\n" +
		"  " + colorGreen + "tree" + colorReset + " [-L N] [path]         Directory tree\r\n" +
		"  " + colorGreen + "grep" + colorReset + " [-in] <pat> <file>    Search file contents\r\n" +
		"  " + colorGreen + "file" + colorReset + " <path>                Identify file type\r\n" +
		"  " + colorGreen + "sort" + colorReset + " [-rnu] <file>         Sort lines\r\n" +
		"  " + colorGreen + "uniq" + colorReset + " [-c] <file>           Deduplicate lines\r\n" +
		"  " + colorGreen + "diff" + colorReset + " <file1> <file2>       Compare files\r\n" +
		"  " + colorGreen + "du" + colorReset + " [path]                  Show directory size\r\n" +
		"  " + colorGreen + "stat" + colorReset + " <path>                Show file details\r\n" +
		"  " + colorGreen + "wc" + colorReset + " [-l] <file>             Count lines/words/bytes\r\n" +
		"  " + colorGreen + "basename" + colorReset + " <path>            File name from path\r\n" +
		"  " + colorGreen + "dirname" + colorReset + " <path>             Directory from path\r\n" +
		"  " + colorGreen + "which" + colorReset + " <cmd>                Locate command\r\n" +
		"  " + colorGreen + "md5sum" + colorReset + " <file>              MD5 checksum\r\n" +
		"  " + colorGreen + "sha256sum" + colorReset + " <file>           SHA-256 checksum\r\n" +
		"  " + colorGreen + "whoami" + colorReset + "                     Current user\r\n" +
		"  " + colorGreen + "date" + colorReset + "                       Current date/time\r\n" +
		"  " + colorGreen + "history" + colorReset + "                    Command history\r\n" +
		"  " + colorGreen + "uname" + colorReset + " [-a]                 System information\r\n" +
		"  " + colorGreen + "ssh" + colorReset + " [user@]host [-p port]   SSH remote connection\r\n" +
		"  " + colorGreen + "fastfetch" + colorReset + "                  System summary\r\n" +
		"  " + colorGreen + "clear" + colorReset + "                      Clear screen\r\n" +
		"  " + colorGreen + "help" + colorReset + "                       Show this help\r\n" +
		"  " + colorGreen + "exit" + colorReset + "                       Close terminal\r\n", nil
}

// --- exit ---

func cmdExit(s *Session, args []string, redirect string) (string, error) {
	return "__exit__", nil
}

// --- shared helpers ---

func readFileContent(s *Session, file string) (string, error) {
	ossPath, _, err := s.resolvePath(file)
	if err != nil {
		return "", err
	}
	data, err := s.ReadFileDecrypted(ossPath)
	if err != nil {
		return "", fmt.Errorf("cannot read: %s", file)
	}
	return string(data), nil
}

func formatSize(size int64) string {
	switch {
	case size >= 1<<30:
		return fmt.Sprintf("%.1fG", float64(size)/float64(1<<30))
	case size >= 1<<20:
		return fmt.Sprintf("%.1fM", float64(size)/float64(1<<20))
	case size >= 1<<10:
		return fmt.Sprintf("%.1fK", float64(size)/float64(1<<10))
	default:
		return fmt.Sprintf("%dB", size)
	}
}

// syncDirDB scans OSS objects under a prefix and upserts them into the files table.
func syncDirDB(s *Session, prefix string) {
	objects, err := s.store.ListAllObjects(prefix)
	if err != nil {
		return
	}
	for _, obj := range objects {
		name := path.Base(strings.TrimSuffix(obj.Key, "/"))
		isDir := strings.HasSuffix(obj.Key, "/")
		ct := ""
		if !isDir {
			ct = mime.TypeByExtension(filepath.Ext(obj.Key))
		}
		_ = s.repos.Files.Upsert(s.UserID, obj.Key, name, isDir, obj.Size, ct, "")
	}
}
