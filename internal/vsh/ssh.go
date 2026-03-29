package vsh

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	ssh_config "github.com/kevinburke/ssh_config"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"zephyr/internal/model"
)

// startSSH initiates an SSH connection from within a vsh session.
// The session switches to "ssh" mode — all subsequent input is forwarded to the remote.
func startSSH(s *Session, args []string) error {
	host, port, user, identityFile, verbose := parseSSHArgs(args)

	// Load SSH config from OSS and apply overrides
	host, port, user, identityFile = applySSHConfig(s, host, port, user, identityFile)

	if host == "" {
		return errors.New("usage: ssh [-v] [user@]host [-p port] [-i identity_file]")
	}
	if port == "" {
		port = "22"
	}
	if user == "" {
		user = s.Username
	}

	// debug helper — only prints when -v is set
	debug := func(format string, a ...any) {
		if verbose {
			s.pushOut(s.ID, fmt.Sprintf("\033[2m  "+format+"\033[0m\r\n", a...))
		}
	}

	debug("Connecting to %s@%s:%s...", user, host, port)

	// Set up auth channel for interactive prompts
	s.authCh = make(chan string, 1)

	// Build auth methods (includes password via interactive prompt)
	authMethods := buildAuthMethods(s, identityFile, debug)

	// Host key callback with interactive confirmation for unknown hosts
	hostKeyCallback := buildHostKeyCallback(s)

	// Connect
	config := &ssh.ClientConfig{
		User:            user,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
	}

	addr := net.JoinHostPort(host, port)
	debug("target: %s@%s", user, addr)

	// Tell frontend to show loading spinner
	if mgr := s.getManager(); mgr != nil && mgr.OnPushSSH != nil {
		mgr.OnPushSSH(s.ConnID, s.ID, "connecting")
	}

	client, err := ssh.Dial("tcp", addr, config)
	s.authCh = nil

	if err != nil {
		if mgr := s.getManager(); mgr != nil && mgr.OnPushSSH != nil {
			mgr.OnPushSSH(s.ConnID, s.ID, "disconnected")
		}
		return fmt.Errorf("connection failed: %v", err)
	}
	if err != nil {
		return fmt.Errorf("connection failed: %v", err)
	}

	session, err := client.NewSession()
	if err != nil {
		client.Close()
		return fmt.Errorf("session failed: %v", err)
	}

	// Request PTY with actual terminal dimensions
	cols, rows := s.Cols, s.Rows
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := session.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		session.Close()
		client.Close()
		return fmt.Errorf("PTY request failed: %v", err)
	}

	// Set up I/O pipes
	stdin, err := session.StdinPipe()
	if err != nil {
		session.Close()
		client.Close()
		return fmt.Errorf("stdin pipe failed: %v", err)
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		client.Close()
		return fmt.Errorf("stdout pipe failed: %v", err)
	}

	// Start shell
	if err := session.Shell(); err != nil {
		session.Close()
		client.Close()
		return fmt.Errorf("shell start failed: %v", err)
	}

	// Switch session to SSH mode
	s.Mode = "ssh"
	s.sshStdin = stdin
	s.sshSesh = session
	s.sshCleanup = func() {
		session.Close()
		client.Close()
	}

	// Notify frontend about SSH mode
	if mgr := s.getManager(); mgr != nil && mgr.OnPushSSH != nil {
		mgr.OnPushSSH(s.ConnID, s.ID, "connected")
	}

	// Read SSH output in a goroutine, push to frontend
	go func() {
		buf := make([]byte, 8192)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				s.pushOut(s.ID, string(buf[:n]))
			}
			if err != nil {
				break
			}
		}
		// SSH session ended
		stopSSH(s)
	}()

	return nil
}

// stopSSH cleans up an SSH session and returns to vsh mode.
func stopSSH(s *Session) {
	if s.Mode != "ssh" {
		return
	}
	if s.sshCleanup != nil {
		s.sshCleanup()
		s.sshCleanup = nil
	}
	s.sshStdin = nil
	s.sshSesh = nil
	s.Mode = "vsh"

	// Notify frontend
	if mgr := s.getManager(); mgr != nil && mgr.OnPushSSH != nil {
		mgr.OnPushSSH(s.ConnID, s.ID, "disconnected")
	}
}

// sshResize sends a window-change request to the remote SSH session.
func sshResize(s *Session, cols, rows int) {
	if s.sshSesh != nil {
		_ = s.sshSesh.WindowChange(rows, cols)
	}
}

// --- Argument parsing ---

func parseSSHArgs(args []string) (host, port, user, identityFile string, verbose bool) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-p":
			if i+1 < len(args) {
				port = args[i+1]
				i++
			}
		case "-i":
			if i+1 < len(args) {
				identityFile = args[i+1]
				i++
			}
		case "-l":
			if i+1 < len(args) {
				user = args[i+1]
				i++
			}
		case "-v", "-vv", "-vvv":
			verbose = true
		default:
			if strings.HasPrefix(args[i], "-") {
				continue
			}
			if at := strings.Index(args[i], "@"); at >= 0 {
				user = args[i][:at]
				host = args[i][at+1:]
			} else {
				host = args[i]
			}
		}
	}
	return
}

// --- SSH config ---

func applySSHConfig(s *Session, host, port, user, identityFile string) (string, string, string, string) {
	configKey := s.Username + "/home/" + s.Username + "/.ssh/config"
	data, err := s.ReadFileDecrypted(configKey)
	if err != nil {
		return host, port, user, identityFile
	}

	cfg, err := ssh_config.Decode(strings.NewReader(string(data)))
	if err != nil {
		return host, port, user, identityFile
	}

	// Use the original alias for all lookups
	alias := host
	if h, _ := cfg.Get(alias, "HostName"); h != "" {
		host = h
	}
	if port == "" {
		if p, _ := cfg.Get(alias, "Port"); p != "" {
			port = p
		}
	}
	if user == "" {
		if u, _ := cfg.Get(alias, "User"); u != "" {
			user = u
		}
	}
	if identityFile == "" {
		if id, _ := cfg.Get(alias, "IdentityFile"); id != "" {
			identityFile = id
		}
	}

	return host, port, user, identityFile
}

// --- Auth methods ---

func buildAuthMethods(s *Session, identityFile string, debug func(string, ...any)) []ssh.AuthMethod {
	var methods []ssh.AuthMethod

	// 1. Try key-based auth
	keyPaths := []string{}
	if identityFile != "" {
		keyPaths = append(keyPaths, identityFile)
	}
	keyPaths = append(keyPaths,
		"/home/"+s.Username+"/.ssh/id_ed25519",
		"/home/"+s.Username+"/.ssh/id_rsa",
		"/home/"+s.Username+"/.ssh/id_ecdsa",
	)

	for _, keyPath := range keyPaths {
		if strings.HasPrefix(keyPath, "~/.ssh/") {
			keyPath = "/home/" + s.Username + "/.ssh/" + keyPath[7:]
		} else if strings.HasPrefix(keyPath, "~/") {
			keyPath = "/home/" + s.Username + "/" + keyPath[2:]
		} else if !strings.HasPrefix(keyPath, "/") {
			keyPath = "/home/" + s.Username + "/.ssh/" + keyPath
		}

		ossKey := s.Username + keyPath
		keyData, err := s.ReadFileDecrypted(ossKey)
		if err != nil {
			debug("key %s: not found", keyPath)
			continue
		}

		signer, err := ssh.ParsePrivateKey(keyData)
		if err != nil {
			debug("key %s: parse error: %v", keyPath, err)
			continue
		}
		debug("key %s: loaded", keyPath)
		methods = append(methods, ssh.PublicKeys(signer))
		break
	}

	// 2. Password auth — prompt user via authCh
	methods = append(methods, ssh.PasswordCallback(func() (string, error) {
		s.pushOut(s.ID, "Password: ")
		password, ok := <-s.authCh
		if !ok {
			return "", errors.New("auth cancelled")
		}
		s.pushOut(s.ID, "\r\n")
		return strings.TrimRight(password, "\r\n"), nil
	}))

	// 3. Keyboard-interactive auth (2FA, custom prompts, etc.)
	methods = append(methods, ssh.KeyboardInteractive(func(user, instruction string, questions []string, echos []bool) ([]string, error) {
		if instruction != "" {
			s.pushOut(s.ID, instruction+"\r\n")
		}
		answers := make([]string, len(questions))
		for i, q := range questions {
			s.pushOut(s.ID, q)
			answer, ok := <-s.authCh
			if !ok {
				return nil, errors.New("auth cancelled")
			}
			if !echos[i] {
				s.pushOut(s.ID, "\r\n")
			}
			answers[i] = strings.TrimRight(answer, "\r\n")
		}
		return answers, nil
	}))

	return methods
}

// --- Known hosts ---

func buildHostKeyCallback(s *Session) ssh.HostKeyCallback {
	knownHostsKey := s.Username + "/home/" + s.Username + "/.ssh/known_hosts"

	// Try to load existing known_hosts from OSS (decrypted)
	var knownCb ssh.HostKeyCallback
	data, err := s.ReadFileDecrypted(knownHostsKey)
	if err == nil && len(data) > 0 {
		tmp, tmpErr := os.CreateTemp("", "zephyr-known-hosts-*")
		if tmpErr == nil {
			tmp.Write(data)
			tmpPath := tmp.Name()
			tmp.Close()
			if cb, parseErr := knownhosts.New(tmpPath); parseErr == nil {
				knownCb = cb
			}
			os.Remove(tmpPath)
		}
	}

	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		// If we have a known_hosts callback, try it first
		if knownCb != nil {
			err := knownCb(hostname, remote, key)
			if err == nil {
				return nil // known host, matches
			}
			// If it's a key mismatch error, reject immediately
			var keyErr *knownhosts.KeyError
			if errors.As(err, &keyErr) && len(keyErr.Want) > 0 {
				s.pushOut(s.ID, "\033[31m@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@\r\n")
				s.pushOut(s.ID, "@ WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED! @\r\n")
				s.pushOut(s.ID, "@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@\033[0m\r\n")
				return errors.New("host key mismatch")
			}
		}

		// Unknown host — ask user to confirm
		fingerprint := ssh.FingerprintSHA256(key)
		s.pushOut(s.ID, fmt.Sprintf(
			"The authenticity of host '%s' can't be established.\r\n%s key fingerprint is %s.\r\nAre you sure you want to continue connecting (yes/no)? ",
			hostname, key.Type(), fingerprint,
		))

		answer, ok := <-s.authCh
		if !ok {
			return errors.New("cancelled")
		}
		answer = strings.TrimSpace(strings.ToLower(answer))
		s.pushOut(s.ID, "\r\n")

		if answer != "yes" && answer != "y" {
			return errors.New("host key not accepted")
		}

		// Save to known_hosts — same flow as cmdMkdir + cmdEcho
		line := knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key)

		// Ensure .ssh directory exists (same as cmdMkdir)
		sshDirOSS := s.Username + "/home/" + s.Username + "/.ssh/"
		if _, dirErr := model.GetFile(s.UserID, sshDirOSS); dirErr != nil {
			_ = s.store.CreateDirectory(sshDirOSS)
			_ = model.UpsertFile(s.UserID, sshDirOSS, ".ssh", true, 0, "", "")
			s.notifyParentDir(sshDirOSS, "created")
		}

		// Read existing content (decrypted)
		existing := ""
		if existData, err2 := s.ReadFileDecrypted(knownHostsKey); err2 == nil {
			existing = string(existData)
		}
		if existing != "" && !strings.HasSuffix(existing, "\n") {
			existing += "\n"
		}

		// Write file encrypted (same as cmdEcho with redirect)
		content := existing + line + "\n"
		_ = s.WriteFileEncrypted(knownHostsKey, []byte(content))
		_ = model.UpsertFile(s.UserID, knownHostsKey, "known_hosts", false, int64(len(content)), "text/plain", "")
		s.notifyParentDir(knownHostsKey, "modified")

		s.pushOut(s.ID, "\033[2mWarning: Permanently added '"+hostname+"' to the list of known hosts.\033[0m\r\n")

		return nil
	}
}

// --- Helpers ---

// getManager retrieves the ShellManager from the session.
func (s *Session) getManager() *ShellManager {
	return s.mgr
}

// portToInt converts a port string to int, defaulting to 22.
func portToInt(port string) int {
	if port == "" {
		return 22
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		return 22
	}
	return p
}
