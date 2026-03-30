package vsh

// Info & utility commands: stat, du, file, basename, dirname, which,
// md5sum, sha256sum, whoami, date, history, uname, fastfetch

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"zephyr/internal/model"
	"zephyr/shared/version"
)

// --- stat ---

func cmdStat(s *Session, args []string, redirect string) (string, error) {
	if len(args) == 0 {
		return "", errors.New("missing file operand")
	}

	ossPath, appPath, err := s.resolvePath(args[0])
	if err != nil {
		return "", err
	}

	info, infoErr := s.store.GetObjectInfo(ossPath)
	if infoErr != nil {
		info, infoErr = s.store.GetObjectInfo(ossPath + "/")
		if infoErr != nil {
			return "", fmt.Errorf("cannot stat '%s': No such file or directory", args[0])
		}
		appPath += "/"
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("  File: %s\r\n", appPath))
	if info.IsDir {
		b.WriteString("  Type: directory\r\n")
	} else {
		b.WriteString("  Type: file\r\n")
		b.WriteString(fmt.Sprintf("  Size: %s (%d bytes)\r\n", formatSize(info.Size), info.Size))
		b.WriteString(fmt.Sprintf("  MIME: %s\r\n", info.ContentType))
	}
	b.WriteString(fmt.Sprintf("Modify: %s\r\n", info.LastModified.Format(time.RFC3339)))
	return b.String(), nil
}

// --- du ---

func cmdDu(s *Session, args []string, redirect string) (string, error) {

	target := s.Cwd
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			target = a
		}
	}

	ossPath, appPath, err := s.resolvePathDir(target)
	if err != nil {
		return "", err
	}

	totalSize, count, err := s.store.GetTotalSize(ossPath)
	if err != nil {
		return "", fmt.Errorf("cannot calculate: %v", err)
	}

	return fmt.Sprintf("%s\t%s (%d files)\r\n", formatSize(totalSize), strings.TrimSuffix(appPath, "/"), count), nil
}

// --- file ---

func cmdFile(s *Session, args []string, redirect string) (string, error) {
	if len(args) == 0 {
		return "", errors.New("missing file operand")
	}

	var b strings.Builder
	for _, file := range args {
		if strings.HasPrefix(file, "-") {
			continue
		}
		ossPath, _, err := s.resolvePath(file)
		if err != nil {
			b.WriteString(fmt.Sprintf("%s: cannot stat\r\n", file))
			continue
		}

		rec, recErr := model.GetFile(s.UserID, ossPath+"/")
		if recErr == nil && rec.IsDir {
			b.WriteString(fmt.Sprintf("%s: directory\r\n", file))
			continue
		}

		rec, recErr = model.GetFile(s.UserID, ossPath)
		if recErr != nil {
			b.WriteString(fmt.Sprintf("%s: cannot stat\r\n", file))
			continue
		}

		ct := rec.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		desc := ct
		switch {
		case strings.HasPrefix(ct, "text/"):
			desc = ct + ", text"
		case strings.HasPrefix(ct, "image/"):
			desc = fmt.Sprintf("%s, %dx%d", ct, rec.MediaWidth, rec.MediaHeight)
		case strings.HasPrefix(ct, "video/"):
			desc = fmt.Sprintf("%s, %.1fs", ct, rec.MediaDuration)
		case strings.HasPrefix(ct, "audio/"):
			desc = fmt.Sprintf("%s, %.1fs", ct, rec.MediaDuration)
		}
		b.WriteString(fmt.Sprintf("%s: %s\r\n", file, desc))
	}
	return b.String(), nil
}

// --- basename / dirname ---

func cmdBasename(s *Session, args []string, redirect string) (string, error) {
	if len(args) == 0 {
		return "", errors.New("missing operand")
	}
	name := path.Base(strings.TrimSuffix(args[0], "/"))
	return name + "\r\n", nil
}

func cmdDirname(s *Session, args []string, redirect string) (string, error) {
	if len(args) == 0 {
		return "", errors.New("missing operand")
	}
	dir := path.Dir(strings.TrimSuffix(args[0], "/"))
	return dir + "\r\n", nil
}

// --- which / type ---

func cmdWhich(s *Session, args []string, redirect string) (string, error) {
	if len(args) == 0 {
		return "", errors.New("missing command name")
	}
	var b strings.Builder
	for _, name := range args {
		if _, ok := commands[name]; ok {
			b.WriteString(fmt.Sprintf("%s: zephyr-vsh built-in\r\n", name))
		} else {
			b.WriteString(fmt.Sprintf("%s: not found\r\n", name))
		}
	}
	return b.String(), nil
}

// --- md5sum ---

func cmdMd5sum(s *Session, args []string, redirect string) (string, error) {
	if len(args) == 0 {
		return "", errors.New("missing file operand")
	}

	var b strings.Builder
	for _, file := range args {
		if strings.HasPrefix(file, "-") {
			continue
		}
		ossPath, _, err := s.resolvePath(file)
		if err != nil {
			return "", err
		}

		rec, recErr := model.GetFile(s.UserID, ossPath)
		if recErr == nil && rec.ContentHash != "" {
			b.WriteString(fmt.Sprintf("%s  %s\r\n", rec.ContentHash, file))
			continue
		}

		data, err := s.ReadFileDecrypted(ossPath)
		if err != nil {
			return "", fmt.Errorf("cannot read: %s", file)
		}
		h := md5.New()
		h.Write(data)
		b.WriteString(fmt.Sprintf("%s  %s\r\n", hex.EncodeToString(h.Sum(nil)), file))
	}
	return b.String(), nil
}

// --- sha256sum ---

func cmdSha256sum(s *Session, args []string, redirect string) (string, error) {
	if len(args) == 0 {
		return "", errors.New("missing file operand")
	}

	var b strings.Builder
	for _, file := range args {
		if strings.HasPrefix(file, "-") {
			continue
		}
		ossPath, _, err := s.resolvePath(file)
		if err != nil {
			return "", err
		}
		data, err := s.ReadFileDecrypted(ossPath)
		if err != nil {
			return "", fmt.Errorf("cannot read: %s", file)
		}
		h := sha256.New()
		h.Write(data)
		b.WriteString(fmt.Sprintf("%s  %s\r\n", hex.EncodeToString(h.Sum(nil)), file))
	}
	return b.String(), nil
}

// --- whoami ---

func cmdWhoami(s *Session, args []string, redirect string) (string, error) {
	return s.Username + "\r\n", nil
}

// --- date ---

func cmdDate(s *Session, args []string, redirect string) (string, error) {
	return time.Now().Format("Mon Jan 2 15:04:05 MST 2006") + "\r\n", nil
}

// --- history ---

func cmdHistory(s *Session, args []string, redirect string) (string, error) {
	key := historyKey(s.Username)
	data, err := s.ReadFileDecrypted(key)
	if err != nil {
		return "", nil
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return "", nil
	}

	var b strings.Builder
	for i, line := range lines {
		b.WriteString(fmt.Sprintf(" %4d  %s\r\n", i+1, line))
	}
	return b.String(), nil
}

// --- uname ---

func cmdUname(s *Session, args []string, redirect string) (string, error) {
	all := false
	for _, a := range args {
		if a == "-a" || a == "--all" {
			all = true
		}
	}
	sysname, nodename, release, machine := unameInfo()
	if all {
		return fmt.Sprintf("%s %s %s %s\r\n", sysname, nodename, release, machine), nil
	}
	return sysname + "\r\n", nil
}

// --- fastfetch / neofetch ---

func cmdFastfetch(s *Session, args []string, redirect string) (string, error) {
	sysname, _, release, machine := unameInfo()

	storageTotal := int64(0)
	storageFiles := 0
	ossPrefix := s.Username + "/"
	if sz, cnt, err := s.store.GetTotalSize(ossPrefix); err == nil {
		storageTotal = sz
		storageFiles = cnt
	}

	cyan := "\033[1;36m"
	blue := "\033[1;34m"
	rst := "\033[0m"
	logo := "" +
		cyan + "       .                  " + rst + "\r\n" +
		cyan + "       |\\                " + rst + "\r\n" +
		cyan + "       | \\               " + rst + "\r\n" +
		cyan + "  .    |  \\              " + rst + "\r\n" +
		cyan + " /|    |   \\             " + rst + "\r\n" +
		cyan + "/ |    |    \\            " + rst + "\r\n" +
		cyan + "'─┴────┴─────'           " + rst + "\r\n" +
		blue + "~·~·~·~·~·~·~·~          " + rst + "\r\n" +
		blue + " ·~·~·~·~·~·~·           " + rst + "\r\n"

	lines := []string{
		fmt.Sprintf("\033[1;32m%s\033[0m@\033[1;34mzephyr\033[0m", s.Username),
		"─────────────────────",
		fmt.Sprintf("\033[1;36mOS\033[0m:      %s %s", sysname, machine),
		fmt.Sprintf("\033[1;36mKernel\033[0m:  %s", release),
		fmt.Sprintf("\033[1;36mShell\033[0m:   zephyr-vsh %s", version.Version),
		fmt.Sprintf("\033[1;36mUser\033[0m:    %s", s.Username),
		fmt.Sprintf("\033[1;36mCWD\033[0m:     %s", s.Cwd),
		fmt.Sprintf("\033[1;36mStorage\033[0m: %s (%d files)", formatSize(storageTotal), storageFiles),
		"",
		"\033[40m  \033[41m  \033[42m  \033[43m  \033[44m  \033[45m  \033[46m  \033[47m  \033[0m",
		"\033[100m  \033[101m  \033[102m  \033[103m  \033[104m  \033[105m  \033[106m  \033[107m  \033[0m",
	}

	logoLines := strings.Split(logo, "\r\n")
	var out strings.Builder
	total := len(logoLines)
	if len(lines) > total {
		total = len(lines)
	}
	for i := 0; i < total; i++ {
		if i < len(logoLines) {
			out.WriteString(logoLines[i])
		}
		if i < len(lines) {
			out.WriteString("  ")
			out.WriteString(lines[i])
		}
		out.WriteString("\r\n")
	}

	return out.String(), nil
}
