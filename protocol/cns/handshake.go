package cns

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"

	TC "github.com/sagernet/sing-box/transport/cns"
)

const defaultKey = "Meng"
const defaultFlag = "httpUDP"
const maxHeaders = 32 * 1024

var established = []byte("HTTP/1.1 200 Connection established\r\nServer: CuteBi Network Tunnel, (%>w<%)\r\nConnection: keep-alive\r\n\r\n")

func defaults(key, flag string) (string, string, error) {
	if key == "" {
		key = defaultKey
	}
	if flag == "" {
		flag = defaultFlag
	}
	if !validToken(key) || strings.IndexFunc(flag, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 || flag == "" {
		return "", "", errors.New("invalid CNS key or UDP flag")
	}
	return key, flag, nil
}

func validToken(value string) bool {
	if value == "" {
		return false
	}
	for _, c := range value {
		if c <= 32 || c >= 127 || strings.ContainsRune("()<>@,;:\\\"/[]?={}", c) {
			return false
		}
	}
	return true
}

func requestHeader(server, target, key, password, flag string, headers http.Header, udp bool) (string, error) {
	if strings.ContainsAny(target, "\r\n") {
		return "", errors.New("invalid CNS destination")
	}
	camouflage := strings.TrimSpace(headers.Get("Host"))
	connectTarget := "/"
	if camouflage != "" {
		if strings.ContainsAny(camouflage, " \t\r\n") {
			return "", errors.New("invalid CNS camouflage host")
		}
		connectTarget = camouflage
		if _, _, err := net.SplitHostPort(camouflage); err != nil {
			connectTarget = net.JoinHostPort(strings.Trim(camouflage, "[]"), "443")
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "CONNECT %s HTTP/1.1\r\n%s: %s\r\n", connectTarget, key, TC.EncryptHost(target, password))
	if udp {
		b.WriteString(flag + "\r\n")
	}
	defaults := http.Header{"Host": {server}, "Dnt": {"1"}, "Connection": {"keep-alive"}}
	for k, values := range headers {
		if !validToken(k) {
			return "", fmt.Errorf("invalid CNS header %q", k)
		}
		for _, v := range values {
			if strings.ContainsAny(v, "\r\n") {
				return "", fmt.Errorf("invalid CNS header %q", k)
			}
		}
		defaults[http.CanonicalHeaderKey(k)] = values
	}
	keys := make([]string, 0, len(defaults))
	for k := range defaults {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range defaults[k] {
			fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
	}
	b.WriteString("\r\n")
	if b.Len() > maxHeaders {
		return "", errors.New("CNS handshake too large")
	}
	return b.String(), nil
}

// The UDP flag is a bare line, so net/http's request parser cannot be used.
func readHeaders(r *bufio.Reader) ([]string, error) {
	var lines []string
	total := 0
	for {
		line, err := r.ReadSlice('\n')
		total += len(line)
		if total > maxHeaders {
			return nil, errors.New("CNS handshake too large")
		}
		if err != nil {
			return nil, err
		}
		text := strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r")
		if text == "" {
			return lines, nil
		}
		lines = append(lines, text)
	}
}

func parseHeaders(lines []string, key, flag, password string) (string, bool, error) {
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "CONNECT ") {
		return "", false, errors.New("invalid CNS request")
	}
	var target string
	udp := false
	for _, line := range lines[1:] {
		if strings.Contains(line, flag) {
			udp = true
		}
		name, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(name, key) && target == "" {
			target = strings.TrimSpace(value)
		}
	}
	if udp {
		return "", true, nil
	}
	if target == "" {
		return "", false, errors.New("missing CNS destination")
	}
	decoded, err := TC.DecryptHost(target, password)
	if err != nil || decoded == "" {
		return "", false, errors.New("invalid CNS destination or password")
	}
	return decoded, udp, nil
}

func writeFull(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if n > 0 {
			p = p[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}
