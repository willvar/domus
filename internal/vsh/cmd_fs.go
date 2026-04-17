package vsh

// File system commands: ls, cd, pwd, mkdir, touch, rm, cp, mv, tree, find

import (
	"errors"
	"fmt"
	"mime"
	"path"
	"path/filepath"
	"strings"

	"zephyr/internal/model"
)

const trashRootPath = "/__trash__/"

func isTrashAppPath(appPath string) bool {
	return appPath == "/__trash__" || appPath == trashRootPath || strings.HasPrefix(appPath, trashRootPath)
}

func trashAppPath(appPath string, isDir bool) string {
	appPath = "/" + strings.TrimPrefix(appPath, "/")
	if appPath == "/" {
		return trashRootPath
	}
	dst := trashRootPath + strings.TrimPrefix(appPath, "/")
	if isDir && !strings.HasSuffix(dst, "/") {
		dst += "/"
	}
	return dst
}

func (s *Session) ensureParentDirRecords(appPath string) error {
	trimmed := strings.Trim(strings.TrimPrefix(appPath, "/"), "/")
	if trimmed == "" {
		return nil
	}

	parts := strings.Split(trimmed, "/")
	for i := 0; i < len(parts)-1; i++ {
		current := "/" + strings.Join(parts[:i+1], "/") + "/"
		if i == 0 && parts[i] == "__trash__" {
			continue
		}
		ossPath := s.Username + current
		if err := s.repos.Files.Upsert(s.UserID, ossPath, parts[i], true, 0, "", ""); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) permanentlyDelete(ossPath string, isDir bool) error {
	if isDir {
		if err := s.store.RecursiveDelete(ossPath, nil); err != nil {
			return err
		}
		_ = s.repos.Files.DeleteByPrefix(s.UserID, ossPath)
		_ = s.repos.Shares.DeleteByPrefix(s.UserID, ossPath)
		return nil
	}

	rec, err := s.repos.Files.Get(s.UserID, ossPath)
	if err != nil {
		return nil
	}
	if rec.Status != "ready" && rec.OSSUploadID != "" {
		_ = s.store.AbortMultipartUpload(ossPath, rec.OSSUploadID)
	}
	if err := s.store.DeleteObject(ossPath); err != nil {
		return err
	}
	_ = s.repos.Files.Delete(s.UserID, ossPath)
	_ = s.repos.Shares.DeleteByPath(s.UserID, ossPath)
	return nil
}

// --- ls ---

func cmdLs(s *Session, args []string, redirect string) (string, error) {

	longFormat := false
	showAll := false
	var target string

	for _, a := range args {
		switch {
		case a == "-l":
			longFormat = true
		case a == "-a":
			showAll = true
		case a == "-la" || a == "-al":
			longFormat = true
			showAll = true
		case !strings.HasPrefix(a, "-"):
			target = a
		}
	}

	dirOSS, _, err := s.resolvePathDir(func() string {
		if target != "" {
			return target
		}
		return s.Cwd
	}())
	if err != nil {
		return "", err
	}

	records, err := s.repos.Files.ListDirectChildren(s.UserID, dirOSS)
	if err != nil {
		return "", fmt.Errorf("cannot list: %v", err)
	}

	if len(records) == 0 {
		return "", nil
	}

	var b strings.Builder
	for _, f := range records {
		if !showAll && strings.HasPrefix(f.Name, ".") {
			continue
		}
		if longFormat {
			perm := "-rw-"
			if f.IsDir {
				perm = "drwx"
			}
			sizeStr := formatSize(f.Size)
			timeStr := f.UpdatedAt.Format("Jan 02 15:04")
			name := f.Name
			if f.IsDir {
				name = colorBlue + name + "/" + colorReset
			}
			fmt.Fprintf(&b, "%s  %8s  %s  %s\r\n", perm, sizeStr, timeStr, name)
		} else {
			if f.IsDir {
				b.WriteString(colorBlue + f.Name + "/" + colorReset + "  ")
			} else {
				b.WriteString(f.Name + "  ")
			}
		}
	}
	if !longFormat {
		b.WriteString("\r\n")
	}
	return b.String(), nil
}

// --- cd ---

func cmdCd(s *Session, args []string, redirect string) (string, error) {
	home := "/home/" + s.Username + "/"
	target := home
	if len(args) > 0 {
		if args[0] == "~" || args[0] == "~/" {
			target = home
		} else if strings.HasPrefix(args[0], "~/") {
			target = home + args[0][2:]
		} else {
			target = args[0]
		}
	}

	_, appPath, err := s.resolvePathDir(target)
	if err != nil {
		return "", err
	}

	// Verify it's a valid directory by checking DB
	dirOSS := s.Username + appPath
	if appPath != "/" {
		records, err := s.repos.Files.ListDirectChildren(s.UserID, dirOSS)
		if err != nil {
			return "", fmt.Errorf("no such directory: %s", appPath)
		}
		// Also check if the directory object itself exists
		_, getErr := s.repos.Files.Get(s.UserID, dirOSS)
		if getErr != nil && len(records) == 0 {
			return "", fmt.Errorf("no such directory: %s", strings.TrimSuffix(appPath, "/"))
		}
	}

	s.Cwd = strings.TrimSuffix(appPath, "/")
	if s.Cwd == "" {
		s.Cwd = "/"
	}
	return "", nil
}

// --- pwd ---

func cmdPwd(s *Session, args []string, redirect string) (string, error) {
	return s.Cwd + "\r\n", nil
}

// --- mkdir ---

func cmdMkdir(s *Session, args []string, redirect string) (string, error) {
	if len(args) == 0 {
		return "", errors.New("missing directory name")
	}

	// Support -p flag (just ignore it since we create parents implicitly with OSS)
	dirs := make([]string, 0, len(args))
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			dirs = append(dirs, a)
		}
	}

	for _, dir := range dirs {
		ossPath, _, err := s.resolvePathDir(dir)
		if err != nil {
			return "", err
		}
		if err := s.store.CreateDirectory(ossPath); err != nil {
			return "", fmt.Errorf("cannot create %s: %v", dir, err)
		}
		name := path.Base(strings.TrimSuffix(ossPath, "/"))
		_ = s.repos.Files.Upsert(s.UserID, ossPath, name, true, 0, "", "")
		s.notifyParentDir(ossPath, "created")
	}
	return "", nil
}

// --- touch ---

func cmdTouch(s *Session, args []string, redirect string) (string, error) {
	if len(args) == 0 {
		return "", errors.New("missing file operand")
	}

	for _, file := range args {
		ossPath, _, err := s.resolvePath(file)
		if err != nil {
			return "", err
		}
		// Check if file already exists
		if _, getErr := s.repos.Files.Get(s.UserID, ossPath); getErr == nil {
			continue // file exists, skip
		}
		wrappedDEK, err := s.WriteFileEncrypted(ossPath, []byte{})
		if err != nil {
			return "", fmt.Errorf("cannot create %s: %v", file, err)
		}
		name := path.Base(ossPath)
		ct := mime.TypeByExtension(filepath.Ext(name))
		_ = s.repos.Files.Upsert(s.UserID, ossPath, name, false, 0, ct, "",
			model.UpsertFileOpts{WrappedDEK: wrappedDEK})
		s.notifyParentDir(ossPath, "created")
	}
	return "", nil
}

// --- rm ---

func cmdRm(s *Session, args []string, redirect string) (string, error) {

	recursive := false
	var targets []string
	for _, a := range args {
		if a == "-r" || a == "-rf" || a == "-fr" {
			recursive = true
		} else if !strings.HasPrefix(a, "-") {
			targets = append(targets, a)
		}
	}
	if len(targets) == 0 {
		return "", errors.New("missing operand")
	}

	for _, t := range targets {
		ossPath, appPath, err := s.resolvePath(t)
		if err != nil {
			return "", err
		}

		// Check if it's a directory
		rec, recErr := s.repos.Files.Get(s.UserID, ossPath+"/")
		isDir := recErr == nil && rec.IsDir
		if !isDir {
			_, recErr = s.repos.Files.Get(s.UserID, ossPath)
		}

		if isTrashAppPath(appPath) {
			targetOSS := ossPath
			if isDir {
				targetOSS += "/"
			}
			if err := s.permanentlyDelete(targetOSS, isDir); err != nil {
				return "", fmt.Errorf("cannot remove %s: %v", t, err)
			}
			s.notifyParentDir(targetOSS, "deleted")
			continue
		}

		if isDir {
			if !recursive {
				return "", fmt.Errorf("cannot remove '%s': Is a directory (use -r)", t)
			}
			dirOSS := ossPath + "/"
			dstApp := trashAppPath(appPath, true)
			dstOSS := s.Username + dstApp
			if err := s.ensureParentDirRecords(dstApp); err != nil {
				return "", fmt.Errorf("cannot remove %s: %v", t, err)
			}
			if err := s.permanentlyDelete(dstOSS, true); err != nil {
				return "", fmt.Errorf("cannot remove %s: %v", t, err)
			}
			if err := s.store.RecursiveMove(dirOSS, dstOSS, nil); err != nil {
				return "", fmt.Errorf("cannot remove %s: %v", t, err)
			}
			_ = s.repos.Files.MoveByPrefix(s.UserID, dirOSS, dstOSS)
			_ = s.repos.Shares.DeleteByPrefix(s.UserID, dirOSS)
			s.notifyParentDir(ossPath, "deleted")
			s.notifyParentDir(dstOSS, "created")
		} else {
			if recErr != nil {
				return "", fmt.Errorf("cannot remove '%s': No such file", t)
			}
			dstApp := trashAppPath(appPath, false)
			dstOSS := s.Username + dstApp
			if err := s.ensureParentDirRecords(dstApp); err != nil {
				return "", fmt.Errorf("cannot remove %s: %v", t, err)
			}
			if err := s.permanentlyDelete(dstOSS, false); err != nil {
				return "", fmt.Errorf("cannot remove %s: %v", t, err)
			}
			if err := s.store.MoveObject(ossPath, dstOSS); err != nil {
				return "", fmt.Errorf("cannot remove %s: %v", t, err)
			}
			_ = s.repos.Files.Move(s.UserID, ossPath, dstOSS, path.Base(dstOSS))
			_ = s.repos.Shares.DeleteByPath(s.UserID, ossPath)
			s.notifyParentDir(ossPath, "deleted")
			s.notifyParentDir(dstOSS, "created")
		}
	}
	return "", nil
}

// --- cp ---

func cmdCp(s *Session, args []string, redirect string) (string, error) {

	recursive := false
	var positional []string
	for _, a := range args {
		if a == "-r" || a == "-R" {
			recursive = true
		} else if !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
		}
	}
	if len(positional) < 2 {
		return "", errors.New("usage: cp [-r] <source> <destination>")
	}

	srcOSS, _, err := s.resolvePath(positional[0])
	if err != nil {
		return "", err
	}
	dstOSS, _, err := s.resolvePath(positional[1])
	if err != nil {
		return "", err
	}

	srcRec, srcErr := s.repos.Files.Get(s.UserID, srcOSS+"/")
	isDir := srcErr == nil && srcRec.IsDir

	if isDir {
		if !recursive {
			return "", fmt.Errorf("omitting directory '%s' (use -r)", positional[0])
		}
		if err := s.store.RecursiveCopy(srcOSS+"/", dstOSS+"/", nil); err != nil {
			return "", fmt.Errorf("copy failed: %v", err)
		}
		syncDirDB(s, dstOSS+"/")
	} else {
		if srcErr != nil {
			if _, err2 := s.repos.Files.Get(s.UserID, srcOSS); err2 != nil {
				return "", fmt.Errorf("cannot stat '%s': No such file", positional[0])
			}
		}
		if err := s.store.CopyObject(srcOSS, dstOSS); err != nil {
			return "", fmt.Errorf("copy failed: %v", err)
		}
		name := path.Base(dstOSS)
		ct := mime.TypeByExtension(filepath.Ext(name))
		srcFile, _ := s.repos.Files.Get(s.UserID, srcOSS)
		size := int64(0)
		var opts []model.UpsertFileOpts
		if srcFile != nil {
			size = srcFile.Size
			if srcFile.WrappedDEK != "" {
				opts = append(opts, model.UpsertFileOpts{WrappedDEK: srcFile.WrappedDEK})
			}
		}
		_ = s.repos.Files.Upsert(s.UserID, dstOSS, name, false, size, ct, "", opts...)
	}
	s.notifyParentDir(dstOSS, "created")
	return "", nil
}

// --- mv ---

func cmdMv(s *Session, args []string, redirect string) (string, error) {

	var positional []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
		}
	}
	if len(positional) < 2 {
		return "", errors.New("usage: mv <source> <destination>")
	}

	srcOSS, _, err := s.resolvePath(positional[0])
	if err != nil {
		return "", err
	}
	dstOSS, _, err := s.resolvePath(positional[1])
	if err != nil {
		return "", err
	}

	srcRec, srcErr := s.repos.Files.Get(s.UserID, srcOSS+"/")
	isDir := srcErr == nil && srcRec.IsDir

	if isDir {
		if err := s.store.RecursiveMove(srcOSS+"/", dstOSS+"/", nil); err != nil {
			return "", fmt.Errorf("move failed: %v", err)
		}
		_ = s.repos.Files.MoveByPrefix(s.UserID, srcOSS+"/", dstOSS+"/")
	} else {
		if _, err2 := s.repos.Files.Get(s.UserID, srcOSS); err2 != nil {
			return "", fmt.Errorf("cannot stat '%s': No such file", positional[0])
		}
		if err := s.store.MoveObject(srcOSS, dstOSS); err != nil {
			return "", fmt.Errorf("move failed: %v", err)
		}
		name := path.Base(dstOSS)
		_ = s.repos.Files.Move(s.UserID, srcOSS, dstOSS, name)
	}
	s.notifyParentDir(srcOSS, "deleted")
	if s.parentOSSDir(srcOSS) != s.parentOSSDir(dstOSS) {
		s.notifyParentDir(dstOSS, "created")
	}
	return "", nil
}

// --- tree ---

func cmdTree(s *Session, args []string, redirect string) (string, error) {

	target := s.Cwd
	maxDepth := 3
	for i := 0; i < len(args); i++ {
		if (args[i] == "-L" || args[i] == "--level") && i+1 < len(args) {
			_, _ = fmt.Sscanf(args[i+1], "%d", &maxDepth)
			i++
		} else if !strings.HasPrefix(args[i], "-") {
			target = args[i]
		}
	}

	ossDir, appDir, err := s.resolvePathDir(target)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString(colorBlue + strings.TrimSuffix(appDir, "/") + colorReset + "\r\n")
	dirs, files := 0, 0
	treeWalk(s, ossDir, "", maxDepth, 0, &b, &dirs, &files)
	fmt.Fprintf(&b, "\r\n%d directories, %d files\r\n", dirs, files)
	return b.String(), nil
}

func treeWalk(s *Session, ossDir, prefix string, maxDepth, depth int, b *strings.Builder, dirs, files *int) {
	if depth >= maxDepth {
		return
	}
	records, err := s.repos.Files.ListDirectChildren(s.UserID, ossDir)
	if err != nil {
		return
	}
	var visible []model.FileRecord
	for _, r := range records {
		if !strings.HasPrefix(r.Name, ".") {
			visible = append(visible, r)
		}
	}
	for i, r := range visible {
		last := i == len(visible)-1
		connector := "├── "
		childPrefix := prefix + "│   "
		if last {
			connector = "└── "
			childPrefix = prefix + "    "
		}
		if r.IsDir {
			*dirs++
			b.WriteString(prefix + connector + colorBlue + r.Name + "/" + colorReset + "\r\n")
			treeWalk(s, ossDir+r.Name+"/", childPrefix, maxDepth, depth+1, b, dirs, files)
		} else {
			*files++
			b.WriteString(prefix + connector + r.Name + "\r\n")
		}
	}
}

// --- find ---

func cmdFind(s *Session, args []string, redirect string) (string, error) {

	searchPath := s.Cwd
	var namePattern string

	for i := 0; i < len(args); i++ {
		if args[i] == "-name" && i+1 < len(args) {
			namePattern = args[i+1]
			i++
		} else if !strings.HasPrefix(args[i], "-") {
			searchPath = args[i]
		}
	}

	if namePattern == "" {
		return "", errors.New("usage: find [path] -name <pattern>")
	}

	ossPath, _, err := s.resolvePathDir(searchPath)
	if err != nil {
		return "", err
	}

	objects, err := s.store.ListAllObjects(ossPath)
	if err != nil {
		return "", fmt.Errorf("search failed: %v", err)
	}

	var b strings.Builder
	prefix := s.Username
	for _, obj := range objects {
		name := path.Base(strings.TrimSuffix(obj.Key, "/"))
		matched, _ := path.Match(namePattern, name)
		if matched {
			appP := strings.TrimPrefix(obj.Key, prefix)
			if !strings.HasPrefix(appP, "/") {
				appP = "/" + appP
			}
			b.WriteString(appP + "\r\n")
		}
	}
	return b.String(), nil
}
