package main

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	server   = "irc.libera.chat:6697"
	nick     = "ggcode-bot"
	proxyURL = "http://192.168.31.16:7890"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: go run register.go <password> verify <code>")
		fmt.Println("       go run register.go <password> register <email>")
		fmt.Println("       go run register.go <password> channel")
		os.Exit(1)
	}
	password := os.Args[1]
	action := os.Args[2]

	fmt.Printf("Connecting via proxy to %s ...\n", server)
	proxy, _ := url.Parse(proxyURL)
	conn, err := dialViaProxy(proxy, server)
	if err != nil {
		fmt.Printf("Connect failed: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	fmt.Fprintf(conn, "NICK %s\r\n", nick)
	fmt.Fprintf(conn, "USER %s 0 * :GGCode Bot\r\n", nick)

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 4096), 512*1024)
	got001 := false
	started := time.Now()

	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r\n")
		if line == "" {
			continue
		}

		// Respond to PING
		if strings.HasPrefix(line, "PING ") {
			prefix := strings.TrimPrefix(line, "PING ")
			fmt.Fprintf(conn, "PONG %s\r\n", prefix)
			continue
		}

		// Show all server messages
		if strings.Contains(line, "ChanServ") || strings.Contains(line, "NickServ") || strings.Contains(line, " 433 ") || strings.Contains(line, "JOIN") || strings.Contains(line, "MODE") {
			fmt.Printf("← %s\n", line)
		}

		// After welcome
		if !got001 && strings.Contains(line, " 001 ") {
			got001 = true
			fmt.Println("Connected. Authenticating...")

			// Identify first
			fmt.Fprintf(conn, "PRIVMSG NickServ :IDENTIFY %s %s\r\n", nick, password)
			fmt.Println("→ IDENTIFY ***")

			time.Sleep(3 * time.Second)

			switch action {
			case "verify":
				if len(os.Args) < 4 {
					fmt.Println("Missing verify code")
					os.Exit(1)
				}
				code := os.Args[3]
				fmt.Fprintf(conn, "PRIVMSG NickServ :VERIFY REGISTER %s %s\r\n", nick, code)
				fmt.Printf("→ VERIFY REGISTER %s ***\n", nick)

			case "register":
				email := os.Args[3]
				fmt.Fprintf(conn, "PRIVMSG NickServ :REGISTER %s %s\r\n", password, email)
				fmt.Printf("→ REGISTER *** ***\n")

			case "channel":
				// Join and register #ggcode
				fmt.Fprintf(conn, "JOIN #ggcode\r\n")
				fmt.Println("→ JOIN #ggcode")
				time.Sleep(5 * time.Second)
				fmt.Fprintf(conn, "PRIVMSG ChanServ :REGISTER #ggcode GGCode official channel\r\n")
				fmt.Println("→ ChanServ REGISTER #ggcode")
			}
		}

		if got001 && time.Since(started) > 25*time.Second {
			break
		}
	}

	fmt.Fprintf(conn, "QUIT :done\r\n")
	time.Sleep(500 * time.Millisecond)
}

func dialViaProxy(proxy *url.URL, target string) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", proxy.Host, 15*time.Second)
	if err != nil {
		return nil, fmt.Errorf("proxy dial: %w", err)
	}

	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)

	reader := bufio.NewReader(conn)
	resp, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(resp, "200") {
		conn.Close()
		return nil, fmt.Errorf("proxy refused: %s", strings.TrimSpace(resp))
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil || strings.TrimSpace(line) == "" {
			break
		}
	}

	tlsConn := tls.Client(conn, &tls.Config{ServerName: "irc.libera.chat"})
	if err := tlsConn.Handshake(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("TLS handshake: %w", err)
	}
	return tlsConn, nil
}
