package protocol

import (
	"bytes"
	"testing"
)

func TestTerminalFrameWireFormat(t *testing.T) {
	got, err := EncodeTerminalFrame(TerminalFrame{Opcode: Input, Slot: 17, Payload: []byte("abc")})
	if err != nil || !bytes.Equal(got, []byte{2, 17, 'a', 'b', 'c'}) {
		t.Fatalf("encoded %v: %v", got, err)
	}
	frame, err := DecodeTerminalFrame([]byte{4, 255, '{', '}'})
	if err != nil || frame.Opcode != Snapshot || frame.Slot != 255 || string(frame.Payload) != "{}" {
		t.Fatalf("decoded %#v: %v", frame, err)
	}
	for _, invalid := range [][]byte{{}, {1}, {0, 1}, {6, 1}} {
		if _, err := DecodeTerminalFrame(invalid); err == nil {
			t.Fatalf("accepted %v", invalid)
		}
	}
}
