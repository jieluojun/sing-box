package clashapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	C "github.com/sagernet/sing-box/constant"
)

func TestVersionEndpoint(t *testing.T) {
	oldVersion, oldCommit := C.Version, C.Commit
	defer func() { C.Version, C.Commit = oldVersion, oldCommit }()
	C.Version = "1.15.0-alpha.13-zero"
	C.Commit = "820679ca49caa62bc0e3e631dd351289cccb2140"
	recorder := httptest.NewRecorder()
	version(recorder, httptest.NewRequest(http.MethodGet, "/version", nil))
	var response struct {
		Version string `json:"version"`
		Premium bool   `json:"premium"`
		Meta    bool   `json:"meta"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || response.Version != "sing-box 1.15.0-alpha.13-820679ca4-zero" || !response.Premium || !response.Meta {
		t.Fatalf("unexpected /version response: status %d, body %s", recorder.Code, recorder.Body.String())
	}
	if C.Version != "1.15.0-alpha.13-zero" {
		t.Fatal("panel version must not alter the shared version")
	}
}
