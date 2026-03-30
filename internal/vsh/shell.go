package vsh

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"sync"

	"github.com/google/uuid"

	"zephyr/internal/auth"
	"zephyr/internal/model"
	"zephyr/internal/store"
)

const (
	historyFileName = ".bash_history"
	maxHistorySize = 500
)


// DirNotifyFunc is called when a command modifies a directory.
type DirNotifyFunc func(resolvedPath, appPath, changeType string)

// PushFunc is a callback for pushing data to the frontend.
type PushFunc func(sessionID, data string)

// PushEventFunc is a callback for pushing structured events to the frontend.
type PushEventFunc func(connID, sessionID string, event string, extra map[string]any)

// sshSession is the interface for SSH session resize support.
type sshSession interface {
	WindowChange(h, w int) error
}

// Session represents an active terminal session (vsh or ssh).
type Session struct {
	ID        string
	UserID    string
	Username  string
	ConnID    string
	Cwd       string // current working directory (app path)
	Mode      string // "vsh" or "ssh"
	Cols      int    // last known terminal width
	Rows      int    // last known terminal height
	EncKey    []byte // per-user KEK (Key Encryption Key) for wrapping/unwrapping per-file DEKs
	store     store.FileStore
	dirNotify DirNotifyFunc
	pushOut   PushFunc  // push output to frontend
	pushDone  PushFunc  // push "command done" to frontend (vsh mode)
	pushExit  PushFunc  // push "session exit" to frontend

	// SSH fields (set when Mode == "ssh")
	sshCleanup func()         // cleanup function to close SSH resources
	sshStdin   io.WriteCloser // remote stdin pipe
	sshSesh    sshSession     // remote session (for resize)
	authCh     chan string     // channel for interactive auth prompts

	mgr *ShellManager // back-reference for push events
}

func (s *Session) notifyDir(ossDir, appDir, changeType string) {
	if s.dirNotify != nil {
		s.dirNotify(ossDir, appDir, changeType)
	}
}

func (s *Session) parentOSSDir(ossPath string) string {
	p := strings.TrimSuffix(ossPath, "/")
	if idx := strings.LastIndex(p, "/"); idx >= 0 {
		return p[:idx+1]
	}
	return s.Username + "/"
}

func (s *Session) toAppPath(ossPath string) string {
	prefix := s.Username + "/"
	if len(ossPath) > len(prefix) && ossPath[:len(prefix)] == prefix {
		return "/" + ossPath[len(prefix):]
	}
	return "/" + ossPath
}

func (s *Session) notifyParentDir(ossPath, changeType string) {
	parent := s.parentOSSDir(ossPath)
	s.notifyDir(parent, s.toAppPath(parent), changeType)
}

// ShellManager manages all terminal sessions.
type ShellManager struct {
	sessions   sync.Map // sessionID → *Session
	store      store.FileStore
	maxPerUser int
	DirNotify  DirNotifyFunc
	// Hub push functions — set externally by cmd/root.go
	OnPushOutput func(connID, sessionID, data string)
	OnPushDone   func(connID, sessionID, cwd string)
	OnPushExit   func(connID, sessionID, reason string)
	OnPushSSH    func(connID, sessionID, status string)
}

func NewShellManager(s store.FileStore) *ShellManager {
	return &ShellManager{
		store:      s,
		maxPerUser: 5,
	}
}

// Open creates a new session.
func (m *ShellManager) Open(userID, username, connID string, cwd string, encKey []byte) (string, error) {
	count := 0
	m.sessions.Range(func(_, v any) bool {
		if v.(*Session).UserID == userID {
			count++
		}
		return count < m.maxPerUser
	})
	if count >= m.maxPerUser {
		return "", errors.New("too_many_sessions")
	}

	if cwd == "" {
		cwd = "/home/" + username + "/"
	}

	id := uuid.New().String()
	s := &Session{
		ID:        id,
		UserID:    userID,
		Username:  username,
		ConnID:    connID,
		Cwd:       cwd,
		Mode:      "vsh",
		EncKey:    encKey,
		store:     m.store,
		dirNotify: m.DirNotify,
		pushOut: func(sid, data string) {
			if m.OnPushOutput != nil {
				m.OnPushOutput(connID, sid, data)
			}
		},
		pushDone: func(sid, cwdVal string) {
			if m.OnPushDone != nil {
				m.OnPushDone(connID, sid, cwdVal)
			}
		},
		pushExit: func(sid, reason string) {
			if m.OnPushExit != nil {
				m.OnPushExit(connID, sid, reason)
			}
		},
		mgr: m,
	}
	m.sessions.Store(id, s)
	return id, nil
}

func (m *ShellManager) Get(id string) *Session {
	v, ok := m.sessions.Load(id)
	if !ok {
		return nil
	}
	return v.(*Session)
}

// Close closes a session, cleaning up SSH if active.
func (m *ShellManager) Close(id string) {
	if s := m.Get(id); s != nil {
		if s.sshCleanup != nil {
			s.sshCleanup()
		}
	}
	m.sessions.Delete(id)
}

// CloseByConn closes all sessions for a WebSocket connection.
func (m *ShellManager) CloseByConn(connID string) {
	m.sessions.Range(func(k, v any) bool {
		s := v.(*Session)
		if s.ConnID == connID {
			if s.sshCleanup != nil {
				s.sshCleanup()
			}
			m.sessions.Delete(k)
		}
		return true
	})
}

// LoadHistory reads the user's command history (encrypted in user space).
func (m *ShellManager) LoadHistory(s *Session) []string {
	key := historyKey(s.Username)
	data, err := s.ReadFileDecrypted(key)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}

// AppendHistory appends a command to the user's persistent history (encrypted in user space).
func (m *ShellManager) AppendHistory(s *Session, cmd string) {
	key := historyKey(s.Username)
	var lines []string
	if existing, err := s.ReadFileDecrypted(key); err == nil {
		text := strings.TrimSpace(string(existing))
		if text != "" {
			lines = strings.Split(text, "\n")
		}
	}
	if len(lines) == 0 || lines[len(lines)-1] != cmd {
		lines = append(lines, cmd)
	}
	if len(lines) > maxHistorySize {
		lines = lines[len(lines)-maxHistorySize:]
	}
	wrappedDEK, err := s.WriteFileEncrypted(key, []byte(strings.Join(lines, "\n")))
	if err == nil && wrappedDEK != "" {
		_ = model.UpsertFile(s.UserID, key, historyFileName, false, 0, "", "",
			model.UpsertFileOpts{WrappedDEK: wrappedDEK})
	}
}

// Input handles input from the frontend. In vsh mode it's a command line,
// in SSH mode it's raw bytes to forward.
func (m *ShellManager) Input(sessionID, data string) {
	s := m.Get(sessionID)
	if s == nil {
		return
	}

	// If waiting for auth input (password, yes/no), route to auth channel
	if s.authCh != nil {
		if data == "\x03" {
			// Ctrl+C: cancel the pending auth/connection
			close(s.authCh)
			s.authCh = nil
			return
		}
		select {
		case s.authCh <- data:
		default:
		}
		return
	}

	if s.Mode == "ssh" {
		if s.sshStdin != nil {
			s.sshStdin.Write([]byte(data))
		}
		return
	}

	// vsh mode: execute command
	m.execVsh(s, data)
}

// execVsh parses and executes a vsh command, pushing output via callbacks.
func (m *ShellManager) execVsh(s *Session, cmdLine string) {
	cmdLine = strings.TrimSpace(cmdLine)
	if cmdLine == "" {
		s.pushDone(s.ID, s.Cwd)
		return
	}

	// Warn about pipes
	if strings.Contains(cmdLine, " | ") {
		s.pushOut(s.ID, "\033[33mPipes (|) are not supported in zephyr-vsh\033[0m\r\n")
		s.pushDone(s.ID, s.Cwd)
		return
	}

	name, args, redirect := parseCmdLine(cmdLine)
	if name == "" {
		s.pushDone(s.ID, s.Cwd)
		return
	}

	// Expand ~ in arguments
	home := "/home/" + s.Username + "/"
	for i, a := range args {
		if a == "~" {
			args[i] = home
		} else if strings.HasPrefix(a, "~/") {
			args[i] = home + a[2:]
		}
	}

	// Warn about glob patterns
	for _, a := range args {
		if !strings.HasPrefix(a, "-") && strings.ContainsAny(a, "*?[") {
			s.pushOut(s.ID, fmt.Sprintf("\033[33mGlob patterns (%s) are not supported, use find -name instead\033[0m\r\n", a))
			s.pushDone(s.ID, s.Cwd)
			return
		}
	}

	// Check for ssh command — special handling
	if name == "ssh" {
		if err := startSSH(s, args); err != nil {
			s.pushOut(s.ID, fmt.Sprintf("\033[31mssh: %s\033[0m\r\n", err))
			s.pushDone(s.ID, s.Cwd)
		}
		// If startSSH succeeded, session is now in ssh mode. No pushDone — SSH takes over.
		return
	}

	fn, ok := commands[name]
	if !ok || fn == nil {
		s.pushOut(s.ID, fmt.Sprintf("\033[31m%s: command not found\033[0m\r\n", name))
		s.pushDone(s.ID, s.Cwd)
		return
	}

	out, err := fn(s, args, redirect)
	if err != nil {
		s.pushOut(s.ID, fmt.Sprintf("\033[31m%s: %s\033[0m\r\n", name, err.Error()))
		s.pushDone(s.ID, s.Cwd)
		return
	}

	if out == "__exit__" {
		s.pushExit(s.ID, "exit")
		return
	}

	if out != "" {
		s.pushOut(s.ID, out)
	}
	s.pushDone(s.ID, s.Cwd)
}

// Complete returns tab-completion candidates (vsh mode only).
func (m *ShellManager) Complete(sessionID, line string) ([]string, string) {
	s := m.Get(sessionID)
	if s == nil || s.Mode != "vsh" {
		return nil, ""
	}

	tokens := tokenize(line)
	trailingSpace := len(line) > 0 && line[len(line)-1] == ' '

	if len(tokens) == 0 || (len(tokens) == 1 && !trailingSpace) {
		prefix := ""
		if len(tokens) == 1 {
			prefix = tokens[0]
		}
		var matches []string
		for name := range commands {
			if strings.HasPrefix(name, prefix) {
				matches = append(matches, name)
			}
		}
		return matches, prefix
	}

	partial := ""
	if !trailingSpace {
		partial = tokens[len(tokens)-1]
	}
	return s.completeFiles(partial), partial
}

// Resize handles terminal resize. Only relevant for SSH mode.
func (m *ShellManager) Resize(sessionID string, cols, rows int) {
	s := m.Get(sessionID)
	if s == nil {
		return
	}
	s.Cols = cols
	s.Rows = rows
	if s.Mode == "ssh" {
		sshResize(s, cols, rows)
	}
}

func (s *Session) completeFiles(partial string) []string {
	dir := ""
	namePrefix := partial
	if idx := strings.LastIndex(partial, "/"); idx >= 0 {
		dir = partial[:idx+1]
		namePrefix = partial[idx+1:]
	}

	listPath := dir
	if listPath == "" {
		listPath = s.Cwd
	}
	ossDir, _, err := s.resolvePathDir(listPath)
	if err != nil {
		return nil
	}

	records, err := model.ListDirectChildren(s.UserID, ossDir)
	if err != nil {
		return nil
	}

	var matches []string
	for _, f := range records {
		if strings.HasPrefix(f.Name, ".") && !strings.HasPrefix(namePrefix, ".") {
			continue
		}
		if strings.HasPrefix(f.Name, namePrefix) {
			candidate := dir + f.Name
			if f.IsDir {
				candidate += "/"
			}
			matches = append(matches, candidate)
		}
	}
	return matches
}

// --- Path resolution ---

func (s *Session) resolvePath(input string) (string, string, error) {
	var appPath string
	if strings.HasPrefix(input, "/") {
		appPath = input
	} else {
		appPath = path.Join(s.Cwd, input)
	}
	appPath = path.Clean(appPath)
	if !strings.HasPrefix(appPath, "/") {
		appPath = "/" + appPath
	}
	if strings.Contains(appPath, "..") {
		return "", "", errors.New("invalid path")
	}
	ossPath := s.Username + appPath
	return ossPath, appPath, nil
}

func (s *Session) resolvePathDir(input string) (string, string, error) {
	ossPath, appPath, err := s.resolvePath(input)
	if err != nil {
		return "", "", err
	}
	if !strings.HasSuffix(ossPath, "/") {
		ossPath += "/"
	}
	if !strings.HasSuffix(appPath, "/") {
		appPath += "/"
	}
	return ossPath, appPath, nil
}

// historyKey returns the OSS key for a user's bash history file.
func historyKey(username string) string {
	return username + "/home/" + username + "/" + historyFileName
}


// ReadFileDecrypted reads a file from OSS and decrypts it using the file's per-file DEK.
func (s *Session) ReadFileDecrypted(ossKey string) ([]byte, error) {
	rc, err := s.store.GetObjectContent(ossKey)
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	if len(s.EncKey) == 0 {
		// No encryption key — read raw
		return io.ReadAll(io.LimitReader(rc, 1<<20))
	}

	// Look up the file's wrapped DEK from DB and unwrap it
	dek, err := s.unwrapFileDEK(ossKey)
	if err != nil {
		return nil, fmt.Errorf("unwrap DEK for %s: %w", ossKey, err)
	}

	var plain bytes.Buffer
	if err := auth.DecryptStream(dek, rc, &plain); err != nil {
		return nil, err
	}
	return plain.Bytes(), nil
}

// WriteFileEncrypted encrypts plaintext with a new per-file DEK and writes to OSS.
// Returns the hex-encoded wrapped DEK (for storing in the file record) and any error.
func (s *Session) WriteFileEncrypted(ossKey string, plaintext []byte) (string, error) {
	if len(s.EncKey) == 0 {
		// No encryption key — write raw
		return "", s.store.PutObjectBytes(ossKey, plaintext)
	}

	// Generate a new per-file DEK
	dek, err := auth.GenerateDEK()
	if err != nil {
		return "", fmt.Errorf("generate DEK: %w", err)
	}

	var cipherBuf bytes.Buffer
	if err := auth.EncryptStream(dek, bytes.NewReader(plaintext), &cipherBuf); err != nil {
		return "", err
	}
	if err := s.store.PutObjectBytes(ossKey, cipherBuf.Bytes()); err != nil {
		return "", err
	}

	// Wrap the DEK with the user's KEK
	wrapped, err := auth.WrapDEK(s.EncKey, dek)
	if err != nil {
		return "", fmt.Errorf("wrap DEK: %w", err)
	}
	return hex.EncodeToString(wrapped), nil
}

// unwrapFileDEK retrieves and unwraps the per-file DEK for the given OSS key.
func (s *Session) unwrapFileDEK(ossKey string) ([]byte, error) {
	rec, err := model.GetFile(s.UserID, ossKey)
	if err != nil {
		return nil, fmt.Errorf("get file record: %w", err)
	}
	if rec.WrappedDEK == "" {
		return nil, fmt.Errorf("no wrapped DEK for file")
	}
	wrappedBytes, err := hex.DecodeString(rec.WrappedDEK)
	if err != nil {
		return nil, fmt.Errorf("decode wrapped DEK: %w", err)
	}
	return auth.UnwrapDEK(s.EncKey, wrappedBytes)
}

// --- Command line parsing ---

func parseCmdLine(line string) (string, []string, string) {
	tokens := tokenize(line)
	if len(tokens) == 0 {
		return "", nil, ""
	}

	var redirect string
	var filtered []string
	for i := 0; i < len(tokens); i++ {
		if tokens[i] == ">>" && i+1 < len(tokens) {
			redirect = ">>" + tokens[i+1]
			i++
		} else if tokens[i] == ">" && i+1 < len(tokens) {
			redirect = tokens[i+1]
			i++
		} else if strings.HasPrefix(tokens[i], ">>") {
			redirect = tokens[i]
		} else if strings.HasPrefix(tokens[i], ">") {
			redirect = strings.TrimPrefix(tokens[i], ">")
		} else {
			filtered = append(filtered, tokens[i])
		}
	}

	if len(filtered) == 0 {
		return "", nil, redirect
	}
	return filtered[0], filtered[1:], redirect
}

func tokenize(s string) []string {
	var tokens []string
	var current strings.Builder
	inSingle := false
	inDouble := false
	escaped := false

	for _, r := range s {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && !inSingle {
			escaped = true
			continue
		}
		if r == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if r == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if r == ' ' && !inSingle && !inDouble {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
			continue
		}
		current.WriteRune(r)
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}
