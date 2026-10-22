package server

import (
	"net/http"
	"strings"
	"testing"
)

func TestShareButtonIsAbsentWhenSharingIsOff(t *testing.T) {
	dir := initTestRepo(t)
	commitFiles(t, dir, "one", map[string]string{"a.txt": "a\n"})
	ts, _ := newTestServer(t, dir)

	body, _ := getBody(t, ts.URL+"/")
	if strings.Contains(body, `id="shareBtn"`) || strings.Contains(body, `id="shareDialog"`) {
		t.Error("share UI rendered while sharing was disabled")
	}
	if strings.Contains(body, "/static/qrcode.js") {
		t.Error("QR library loaded while sharing was disabled")
	}
}

func TestShareDialogCarriesConfiguredOrigins(t *testing.T) {
	dir := initTestRepo(t)
	commitFiles(t, dir, "one", map[string]string{"a.txt": "a\n"})
	ts, _ := newTestServerWithOptions(t, dir, Options{
		Version: "test",
		Share: ShareOptions{
			Enabled:   true,
			PublicURL: "http://strowger.mule-bramble.b7r.dev:8080",
			Origins: []string{
				"http://strowger.mule-bramble.b7r.dev:8080",
				"http://192.168.1.5:8080",
			},
		},
	})

	body, _ := getBody(t, ts.URL+"/")
	for _, want := range []string{
		`id="shareBtn"`,
		`id="shareDialog"`,
		`data-public-url="http://strowger.mule-bramble.b7r.dev:8080"`,
		`value="http://strowger.mule-bramble.b7r.dev:8080"`,
		`data-origin="http://strowger.mule-bramble.b7r.dev:8080"`,
		`data-origin="http://192.168.1.5:8080"`,
		"/static/qrcode.js?v=",
		"Anyone who can reach these addresses can read this repository",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("share page missing %q", want)
		}
	}
}

func TestShareQRCodeLibraryIsEmbedded(t *testing.T) {
	dir := initTestRepo(t)
	commitFiles(t, dir, "one", map[string]string{"a.txt": "a\n"})
	ts, _ := newTestServer(t, dir)

	status, hdr, body := doReq(t, ts.URL+"/static/qrcode.js", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if ct := hdr.Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("content type = %q, want javascript", ct)
	}
	if !strings.Contains(body, "QR Code Generator") {
		t.Error("qrcode.js did not serve the vendored encoder")
	}
}
