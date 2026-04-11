package vsh

// Text processing commands: cat, head, tail, echo, grep, sort, uniq, diff, wc

import (
	"errors"
	"fmt"
	"mime"
	"path"
	"path/filepath"
	gosort "sort"
	"strings"

	"zephyr/internal/model"
)

// --- cat ---

func cmdCat(s *Session, args []string, redirect string) (string, error) {
	if len(args) == 0 {
		return "", errors.New("missing file operand")
	}

	ossPath, _, err := s.resolvePath(args[0])
	if err != nil {
		return "", err
	}

	data, err := s.ReadFileDecrypted(ossPath)
	if err != nil {
		return "", fmt.Errorf("cannot read: %s", args[0])
	}

	const maxCat = 1 << 20

	truncated := len(data) > maxCat
	if truncated {
		data = data[:maxCat]
	}

	content := string(data)
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\n", "\r\n")
	if !strings.HasSuffix(content, "\r\n") {
		content += "\r\n"
	}
	if truncated {
		content += colorYellow + "[Output truncated at 1MB, use head/tail to read specific parts]" + colorReset + "\r\n"
	}
	return content, nil
}

// --- head ---

func cmdHead(s *Session, args []string, redirect string) (string, error) {

	n := 10
	var file string
	for i := 0; i < len(args); i++ {
		if args[i] == "-n" && i+1 < len(args) {
			_, _ = fmt.Sscanf(args[i+1], "%d", &n)
			i++
		} else if !strings.HasPrefix(args[i], "-") {
			file = args[i]
		}
	}
	if file == "" {
		return "", errors.New("missing file operand")
	}

	content, err := readFileContent(s, file)
	if err != nil {
		return "", err
	}

	lines := strings.SplitN(content, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	result := strings.Join(lines, "\r\n")
	if !strings.HasSuffix(result, "\r\n") {
		result += "\r\n"
	}
	return result, nil
}

// --- tail ---

func cmdTail(s *Session, args []string, redirect string) (string, error) {

	n := 10
	var file string
	for i := 0; i < len(args); i++ {
		if args[i] == "-n" && i+1 < len(args) {
			_, _ = fmt.Sscanf(args[i+1], "%d", &n)
			i++
		} else if !strings.HasPrefix(args[i], "-") {
			file = args[i]
		}
	}
	if file == "" {
		return "", errors.New("missing file operand")
	}

	content, err := readFileContent(s, file)
	if err != nil {
		return "", err
	}

	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	result := strings.Join(lines, "\r\n")
	if !strings.HasSuffix(result, "\r\n") {
		result += "\r\n"
	}
	return result, nil
}

// --- echo ---

func cmdEcho(s *Session, args []string, redirect string) (string, error) {
	text := strings.Join(args, " ")

	if redirect != "" {

		appendMode := strings.HasPrefix(redirect, ">>")
		target := redirect
		if appendMode {
			target = strings.TrimPrefix(redirect, ">>")
		}

		ossPath, _, err := s.resolvePath(target)
		if err != nil {
			return "", err
		}

		content := text + "\n"
		if appendMode {
			if existing, readErr := s.ReadFileDecrypted(ossPath); readErr == nil {
				content = string(existing) + content
			}
		}

		wrappedDEK, err := s.WriteFileEncrypted(ossPath, []byte(content))
		if err != nil {
			return "", fmt.Errorf("write failed: %v", err)
		}
		name := path.Base(ossPath)
		ct := mime.TypeByExtension(filepath.Ext(name))
		_ = s.repos.Files.Upsert(s.UserID, ossPath, name, false, int64(len(content)), ct, "",
			model.UpsertFileOpts{WrappedDEK: wrappedDEK})
		s.notifyParentDir(ossPath, "modified")
		return "", nil
	}

	return text + "\r\n", nil
}

// --- grep ---

func cmdGrep(s *Session, args []string, redirect string) (string, error) {

	caseInsensitive := false
	lineNumbers := false
	var positional []string
	for _, a := range args {
		switch a {
		case "-i":
			caseInsensitive = true
		case "-n":
			lineNumbers = true
		case "-in", "-ni":
			caseInsensitive = true
			lineNumbers = true
		default:
			if !strings.HasPrefix(a, "-") {
				positional = append(positional, a)
			}
		}
	}
	if len(positional) < 2 {
		return "", errors.New("usage: grep [-i] [-n] <pattern> <file>")
	}

	pattern := positional[0]
	file := positional[1]

	content, err := readFileContent(s, file)
	if err != nil {
		return "", err
	}

	lines := strings.Split(content, "\n")
	var b strings.Builder
	matchPattern := pattern
	if caseInsensitive {
		matchPattern = strings.ToLower(pattern)
	}

	for i, line := range lines {
		testLine := line
		if caseInsensitive {
			testLine = strings.ToLower(line)
		}
		if strings.Contains(testLine, matchPattern) {
			if lineNumbers {
				fmt.Fprintf(&b, "\033[32m%d\033[0m:", i+1)
			}
			remaining := line
			for {
				searchIn := remaining
				if caseInsensitive {
					searchIn = strings.ToLower(remaining)
				}
				pos := strings.Index(searchIn, matchPattern)
				if pos < 0 {
					b.WriteString(remaining)
					break
				}
				b.WriteString(remaining[:pos])
				b.WriteString("\033[1;31m")
				b.WriteString(remaining[pos : pos+len(pattern)])
				b.WriteString("\033[0m")
				remaining = remaining[pos+len(pattern):]
			}
			b.WriteString("\r\n")
		}
	}
	return b.String(), nil
}

// --- sort ---

func cmdSort(s *Session, args []string, redirect string) (string, error) {

	reverse := false
	numeric := false
	unique := false
	var file string
	for _, a := range args {
		switch a {
		case "-r":
			reverse = true
		case "-n":
			numeric = true
		case "-u":
			unique = true
		case "-rn", "-nr":
			reverse = true
			numeric = true
		default:
			if !strings.HasPrefix(a, "-") {
				file = a
			}
		}
	}
	if file == "" {
		return "", errors.New("missing file operand")
	}

	content, err := readFileContent(s, file)
	if err != nil {
		return "", err
	}

	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")

	if numeric {
		gosort.Slice(lines, func(i, j int) bool {
			var a, b float64
			_, _ = fmt.Sscanf(lines[i], "%f", &a)
			_, _ = fmt.Sscanf(lines[j], "%f", &b)
			if reverse {
				return a > b
			}
			return a < b
		})
	} else {
		gosort.Strings(lines)
		if reverse {
			for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
				lines[i], lines[j] = lines[j], lines[i]
			}
		}
	}

	if unique {
		deduped := lines[:0]
		for i, l := range lines {
			if i == 0 || l != lines[i-1] {
				deduped = append(deduped, l)
			}
		}
		lines = deduped
	}

	return strings.Join(lines, "\r\n") + "\r\n", nil
}

// --- uniq ---

func cmdUniq(s *Session, args []string, redirect string) (string, error) {

	countMode := false
	var file string
	for _, a := range args {
		if a == "-c" {
			countMode = true
		} else if !strings.HasPrefix(a, "-") {
			file = a
		}
	}
	if file == "" {
		return "", errors.New("missing file operand")
	}

	content, err := readFileContent(s, file)
	if err != nil {
		return "", err
	}

	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	var b strings.Builder
	count := 1
	for i := 1; i <= len(lines); i++ {
		if i < len(lines) && lines[i] == lines[i-1] {
			count++
			continue
		}
		if countMode {
			fmt.Fprintf(&b, "%4d %s\r\n", count, lines[i-1])
		} else {
			b.WriteString(lines[i-1] + "\r\n")
		}
		count = 1
	}
	return b.String(), nil
}

// --- diff ---

func cmdDiff(s *Session, args []string, redirect string) (string, error) {

	var positional []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
		}
	}
	if len(positional) < 2 {
		return "", errors.New("usage: diff <file1> <file2>")
	}

	content1, err := readFileContent(s, positional[0])
	if err != nil {
		return "", err
	}
	content2, err := readFileContent(s, positional[1])
	if err != nil {
		return "", err
	}

	if content1 == content2 {
		return "", nil
	}

	lines1 := strings.Split(content1, "\n")
	lines2 := strings.Split(content2, "\n")

	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\r\n+++ %s\r\n", positional[0], positional[1])

	maxLen := len(lines1)
	if len(lines2) > maxLen {
		maxLen = len(lines2)
	}
	for i := 0; i < maxLen; i++ {
		l1, l2 := "", ""
		if i < len(lines1) {
			l1 = lines1[i]
		}
		if i < len(lines2) {
			l2 = lines2[i]
		}
		if l1 != l2 {
			if i < len(lines1) {
				b.WriteString(colorRed + "- " + l1 + colorReset + "\r\n")
			}
			if i < len(lines2) {
				b.WriteString(colorGreen + "+ " + l2 + colorReset + "\r\n")
			}
		}
	}
	return b.String(), nil
}

// --- wc ---

func cmdWc(s *Session, args []string, redirect string) (string, error) {

	linesOnly := false
	var file string
	for _, a := range args {
		if a == "-l" {
			linesOnly = true
		} else if !strings.HasPrefix(a, "-") {
			file = a
		}
	}
	if file == "" {
		return "", errors.New("missing file operand")
	}

	content, err := readFileContent(s, file)
	if err != nil {
		return "", err
	}

	lineCount := strings.Count(content, "\n")
	if linesOnly {
		return fmt.Sprintf("%d %s\r\n", lineCount, file), nil
	}

	words := len(strings.Fields(content))
	bytes := len(content)
	return fmt.Sprintf("%d %d %d %s\r\n", lineCount, words, bytes, file), nil
}
