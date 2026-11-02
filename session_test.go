package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateSessionRequest(t *testing.T) {
	valid := SessionRequest{
		Board:    "kc868-a6",
		FlashMB:  4,
		WifiSpec: "ssid=ESP-Lab,psk=esplab123",
		Script:   "2.20 uart0 Backlog SSID1 ESP-Lab\n",
	}
	if err := validateSessionRequest(valid); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}

	tests := []struct {
		name   string
		change func(*SessionRequest)
	}{
		{"empty board", func(r *SessionRequest) { r.Board = "" }},
		{"bad flash", func(r *SessionRequest) { r.FlashMB = 0 }},
		{"no wifi", func(r *SessionRequest) { r.WifiSpec = "" }},
		{"no script", func(r *SessionRequest) { r.Script = "" }},
	}
	for _, test := range tests {
		request := valid
		test.change(&request)
		if err := validateSessionRequest(request); err == nil {
			t.Errorf("%s: expected an error", test.name)
		}
	}
}

func TestWriteFlashPadsToSize(t *testing.T) {
	directory := t.TempDir()
	factory := filepath.Join(directory, "factory.bin")
	if err := os.WriteFile(factory, []byte{1, 2, 3}, 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(directory, "flash.bin")
	if err := writeFlash(factory, target, 1); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 1024*1024 {
		t.Fatalf("size = %d, want %d", info.Size(), 1024*1024)
	}
	bytes, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if bytes[0] != 1 || bytes[2] != 3 || bytes[3] != 0xff || bytes[len(bytes)-1] != 0xff {
		t.Fatal("flash contents are not the factory image followed by 0xff padding")
	}
}

func TestLoopbackOnly(t *testing.T) {
	for _, address := range []string{"127.0.0.1:8788", "localhost:8788", "[::1]:8788"} {
		if err := loopbackOnly(address); err != nil {
			t.Errorf("%s rejected: %v", address, err)
		}
	}
	for _, address := range []string{"0.0.0.0:8788", "192.168.1.10:8788"} {
		if err := loopbackOnly(address); err == nil {
			t.Errorf("%s accepted", address)
		}
	}
}

func TestLoopbackHost(t *testing.T) {
	for _, host := range []string{"127.0.0.1:8788", "localhost:8788", "[::1]:8788"} {
		if !loopbackHost(host) {
			t.Errorf("%s rejected", host)
		}
	}
	for _, host := range []string{"example.com:8788", "10.0.0.1:8788"} {
		if loopbackHost(host) {
			t.Errorf("%s accepted", host)
		}
	}
}
