package http

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
)

func TestConnectTargetOptions(t *testing.T) {
	tests := []struct {
		name string
		options ClientOptions
		line string
		host string
		userAgent bool
	}{
		{"with-at", ClientOptions{Headers: http.Header{"With-At": {"free.example"}}}, "CONNECT example.com:443@free.example HTTP/1.1", "example.com:443", true},
		{"baidu", ClientOptions{Headers: http.Header{"Baidu-Direct": {"true"}}}, "CONNECT example.com:443HTTP/1.1", "example.com:443", true},
		{"path-wins", ClientOptions{Path: "@path", Headers: http.Header{"With-At": {"free.example"}, "Host": {"custom.example"}}}, "CONNECT example.com:443@path HTTP/1.1", "custom.example", true},
		{"del-host", ClientOptions{DelHost: true}, "CONNECT example.com:443 HTTP/1.1", "", false},
		{"custom-host", ClientOptions{DelHost: true, Headers: http.Header{"Host": {"custom.example"}}}, "CONNECT example.com:443 HTTP/1.1", "custom.example", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.options.ConnectTargetOptions = true
			client, err := NewClient(tt.options)
			if err != nil { t.Fatal(err) }
			req := &http.Request{Header: buildRequestHeader(client.headers, client.authorization, false)}
			var output bytes.Buffer
			if err = client.writeConnectRequest(&output, req, "example.com:443"); err != nil { t.Fatal(err) }
			text := output.String()
			if !strings.HasPrefix(text, tt.line+"\r\n") { t.Fatalf("unexpected request: %q", text) }
			if strings.Contains(text, "With-At:") || strings.Contains(text, "Baidu-Direct:") { t.Fatalf("pseudo header leaked: %q", text) }
			if got := strings.Contains(text, "Host: "+tt.host+"\r\n"); tt.host != "" && !got { t.Fatalf("missing Host: %q", text) }
			if tt.host == "" && strings.Contains(text, "Host:") { t.Fatalf("unexpected Host: %q", text) }
			if strings.Contains(text, "User-Agent:") != tt.userAgent { t.Fatalf("unexpected User-Agent: %q", text) }
		})
	}
}

func TestConnectTargetRejectsInjection(t *testing.T) {
	_, err := NewClient(ClientOptions{ConnectTargetOptions: true, Path: "@ok\r\nInjected: value"})
	if err == nil { t.Fatal("expected invalid suffix error") }
}
