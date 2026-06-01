package wasm

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	tankmaze "github.com/tankmaze/sdk"
)

func TestVerifyChecksum(t *testing.T) {
	content := []byte("hello wasm")
	sum := sha256.Sum256(content)
	correctHex := hex.EncodeToString(sum[:])

	tmp := filepath.Join(t.TempDir(), "test.wasm")
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := VerifyChecksum(tmp, correctHex); err != nil {
		t.Fatalf("expected no error on correct checksum, got: %v", err)
	}

	if err := VerifyChecksum(tmp, "deadbeef"); err == nil {
		t.Fatal("expected error on wrong checksum, got nil")
	}

	if err := VerifyChecksum("/no/such/file", correctHex); err == nil {
		t.Fatal("expected error on missing file, got nil")
	}
}

func TestDecodeAction(t *testing.T) {
	cases := []struct {
		encoded uint32
		want    tankmaze.Action
	}{
		{0, tankmaze.Action{Type: tankmaze.Idle, Direction: tankmaze.Forward}},
		{10, tankmaze.Action{Type: tankmaze.Move, Direction: tankmaze.Forward}},
		{11, tankmaze.Action{Type: tankmaze.Move, Direction: tankmaze.Backward}},
		{12, tankmaze.Action{Type: tankmaze.Move, Direction: tankmaze.Left}},
		{13, tankmaze.Action{Type: tankmaze.Move, Direction: tankmaze.Right}},
		{20, tankmaze.Action{Type: tankmaze.Rotate, Direction: tankmaze.Forward}},
		{22, tankmaze.Action{Type: tankmaze.Rotate, Direction: tankmaze.Left}},
		{23, tankmaze.Action{Type: tankmaze.Rotate, Direction: tankmaze.Right}},
		{30, tankmaze.Action{Type: tankmaze.Fire, Direction: tankmaze.Forward}},
		{40, tankmaze.Action{Type: tankmaze.Scan, Direction: tankmaze.Forward}},
	}
	for _, c := range cases {
		got := decodeAction(c.encoded)
		if got != c.want {
			t.Errorf("decodeAction(%d) = %+v, want %+v", c.encoded, got, c.want)
		}
	}
}
