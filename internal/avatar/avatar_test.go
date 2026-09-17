package avatar

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestNormalizeDataURL(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x00}
	value, err := NormalizeDataURL("data:image/png;base64," + base64.StdEncoding.EncodeToString(png))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(value, "data:image/png;base64,") {
		t.Fatalf("unexpected normalized avatar: %q", value)
	}
}

func TestNormalizeDataURLRejectsSVG(t *testing.T) {
	_, err := NormalizeDataURL("data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte("<svg/>")))
	if err == nil {
		t.Fatal("expected SVG avatar to be rejected")
	}
}
