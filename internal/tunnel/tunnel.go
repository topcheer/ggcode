package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Event represents a JSON event from localhost.run.
type Event struct {
	ConnectionID string `json:"connection_id"`
	Event        string `json:"event"`
	Message      string `json:"message"`
	URL          string `json:"url,omitempty"`
}

// Tunnel manages an SSH reverse tunnel to localhost.run using pure Go.
type Tunnel struct {
	localPort int
	netConn   net.Conn
	sshConn   *ssh.Client
	url       string
	mu        sync.RWMutex
	done      chan struct{}
	onDead    func()
}

// New creates a new tunnel for the given local port.
func New(localPort int) *Tunnel {
	return &Tunnel{localPort: localPort}
}

// OnDead registers a callback for when the tunnel dies.
func (t *Tunnel) OnDead(fn func()) {
	t.onDead = fn
}

// Start establishes the SSH reverse tunnel and returns the public URL.
func (t *Tunnel) Start(ctx context.Context) (string, error) {
	localAddr := fmt.Sprintf("127.0.0.1:%d", t.localPort)

	// Verify local port
	if c, err := net.DialTimeout("tcp", localAddr, 2*time.Second); err != nil {
		return "", fmt.Errorf("no service on %s: %w", localAddr, err)
	} else {
		c.Close()
	}

	auth, err := loadSSHKey()
	if err != nil {
		return "", err
	}

	config := &ssh.ClientConfig{
		User:            os.Getenv("USER"),
		Auth:            []ssh.AuthMethod{auth},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
	}

	dialer := net.Dialer{Timeout: 15 * time.Second}
	netConn, err := dialer.DialContext(ctx, "tcp", "ssh.localhost.run:22")
	if err != nil {
		return "", fmt.Errorf("connect ssh.localhost.run: %w", err)
	}
	t.netConn = netConn

	// SSH handshake — get raw connection, channels, and global requests
	sshConn, chans, globalReqs, err := ssh.NewClientConn(netConn, "ssh.localhost.run:22", config)
	if err != nil {
		netConn.Close()
		return "", fmt.Errorf("ssh handshake: %w", err)
	}

	t.done = make(chan struct{})

	// DO NOT use ssh.NewClient — it consumes the chans channel.
	// Instead, handle channels ourselves so we can proxy forwarded-tcpip.
	go t.handleChannels(chans, localAddr)
	go ssh.DiscardRequests(globalReqs)

	// Open a session channel manually
	ch, sessReqs, err := sshConn.OpenChannel("session", nil)
	if err != nil {
		sshConn.Close()
		return "", fmt.Errorf("open session: %w", err)
	}
	session := &sshSession{ch: ch, reqs: sessReqs}

	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		sshConn.Close()
		return "", err
	}

	// Request PTY for the session
	session.SendRequest("pty-req", true, ssh.Marshal(struct {
		Term    string
		Width   uint32
		Height  uint32
		MWidth  uint32
		MHeight uint32
		Modes   string
	}{"xterm", 80, 24, 800, 600, ""}))

	// Request reverse forwarding
	ok, _, err := sshConn.SendRequest("tcpip-forward", true, ssh.Marshal(struct {
		Addr string
		Port uint32
	}{Addr: "", Port: 80}))
	if err != nil || !ok {
		session.Close()
		sshConn.Close()
		return "", fmt.Errorf("tcpip-forward denied: %v", err)
	}

	if err := session.Shell(); err != nil {
		session.Close()
		sshConn.Close()
		return "", err
	}

	// Read URL
	urlCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		buf := make([]byte, 8192)
		for {
			n, rerr := stdout.Read(buf)
			if rerr != nil {
				if rerr != io.EOF {
					select {
					case errCh <- rerr:
					default:
					}
				}
				return
			}
			for _, line := range strings.Split(string(buf[:n]), "\n") {
				line = strings.TrimSpace(line)
				if u := extractURL(line); u != "" {
					t.mu.Lock()
					t.url = u
					t.mu.Unlock()
					select {
					case urlCh <- u:
					default:
					}
				}
				if strings.HasPrefix(line, "{") {
					var evt Event
					if json.Unmarshal([]byte(line), &evt) == nil {
						if u := extractURL(evt.Message); u != "" {
							t.mu.Lock()
							t.url = u
							t.mu.Unlock()
							select {
							case urlCh <- u:
							default:
							}
						}
					}
				}
			}
		}
	}()

	// Monitor health
	go func() {
		sshConn.Wait()
		log.Printf("[tunnel] connection lost")
		close(t.done)
		if t.onDead != nil {
			t.onDead()
		}
	}()

	select {
	case url := <-urlCh:
		return url, nil
	case err := <-errCh:
		t.Stop()
		return "", fmt.Errorf("session: %w", err)
	case <-ctx.Done():
		t.Stop()
		return "", ctx.Err()
	}
}

// handleChannels dispatches incoming SSH channels.
// forwarded-tcpip channels are proxied to the local gateway.
func (t *Tunnel) handleChannels(chans <-chan ssh.NewChannel, localAddr string) {
	for ch := range chans {
		switch ch.ChannelType() {
		case "forwarded-tcpip":
			go t.proxyChannel(ch, localAddr)
		case "session":
			// Sessions are handled by sshConn.NewSession() — shouldn't arrive here
			ch.Reject(ssh.Prohibited, "unexpected session channel")
		default:
			ch.Reject(ssh.UnknownChannelType, fmt.Sprintf("unknown: %s", ch.ChannelType()))
		}
	}
}

func (t *Tunnel) proxyChannel(nc ssh.NewChannel, localAddr string) {
	ch, reqs, err := nc.Accept()
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)

	conn, err := net.DialTimeout("tcp", localAddr, 5*time.Second)
	if err != nil {
		ch.Close()
		return
	}

	var once sync.Once
	cleanup := func() {
		once.Do(func() { ch.Close(); conn.Close() })
	}
	go func() { io.Copy(ch, conn); cleanup() }()
	go func() { io.Copy(conn, ch); cleanup() }()
}

func (t *Tunnel) Stop() {
	if t.netConn != nil {
		t.netConn.Close()
	}
	t.mu.Lock()
	t.url = ""
	t.mu.Unlock()
}

func (t *Tunnel) URL() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.url
}

func (t *Tunnel) Alive() bool {
	if t.done == nil {
		return false
	}
	select {
	case <-t.done:
		return false
	default:
		return true
	}
}

func extractURL(msg string) string {
	for _, part := range strings.Fields(msg) {
		if strings.HasPrefix(part, "https://") {
			return strings.TrimRight(part, ",;")
		}
	}
	return ""
}

func loadSSHKey() (ssh.AuthMethod, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"id_ed25519", "id_rsa", "id_ecdsa"} {
		data, err := os.ReadFile(filepath.Join(home, ".ssh", name))
		if err != nil {
			continue
		}
		signer, err := ssh.ParsePrivateKey(data)
		if err != nil {
			continue
		}
		return ssh.PublicKeys(signer), nil
	}
	return nil, fmt.Errorf("no SSH key found in ~/.ssh")
}

// sshSession wraps a raw SSH channel to provide session-like methods.
type sshSession struct {
	ch   ssh.Channel
	reqs <-chan *ssh.Request
}

func (s *sshSession) Close() error { return s.ch.Close() }

func (s *sshSession) StdoutPipe() (io.Reader, error) { return s.ch, nil }

func (s *sshSession) StderrPipe() (io.Reader, error) { return s.ch.Stderr(), nil }

func (s *sshSession) SendRequest(name string, wantReply bool, payload []byte) (bool, error) {
	return s.ch.SendRequest(name, wantReply, payload)
}

func (s *sshSession) Shell() error {
	_, err := s.ch.SendRequest("shell", true, nil)
	go ssh.DiscardRequests(s.reqs)
	return err
}
