package wol

import (
	"bytes"
	"net"
	"testing"
)

func TestParseMAC(t *testing.T) {
	want := net.HardwareAddr{0x3c, 0x7c, 0x3f, 0x1a, 0x2b, 0x4d}
	for _, in := range []string{"3C-7C-3F-1A-2B-4D", "3c:7c:3f:1a:2b:4d", "3C7C3F1A2B4D"} {
		got, err := ParseMAC(in)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("ParseMAC(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := ParseMAC("zz-zz"); err == nil {
		t.Error("bad MAC accepted")
	}
	if got := FormatMAC("3c:7c:3f:1a:2b:4d"); got != "3C-7C-3F-1A-2B-4D" {
		t.Errorf("FormatMAC = %q", got)
	}
}

func TestMagicPacket(t *testing.T) {
	mac := net.HardwareAddr{1, 2, 3, 4, 5, 6}
	p := MagicPacket(mac)
	if len(p) != 102 {
		t.Fatalf("len = %d", len(p))
	}
	if !bytes.Equal(p[:6], []byte{255, 255, 255, 255, 255, 255}) {
		t.Fatal("bad header")
	}
	for i := 0; i < 16; i++ {
		if !bytes.Equal(p[6+i*6:12+i*6], mac) {
			t.Fatalf("repetition %d wrong", i)
		}
	}
}

func TestSendReachesListener(t *testing.T) {
	// Listen on the WoL port if we can (may need privileges); otherwise just
	// make sure Send does not error.
	if err := Send("01-02-03-04-05-06", "127.0.0.1"); err != nil {
		t.Fatalf("Send: %v", err)
	}
}
