package client

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestUploadFileSendsCorrelatedMetadataAndBoundedBinaryFrames(t *testing.T) {
	data := bytes.Repeat([]byte{0, 0xff, 0x10, 0x7f}, 40000)
	url := testServer(t, func(conn *websocket.Conn) {
		handshake(t, conn)
		request := receive(t, conn)["message"].(map[string]any)
		if request["type"] != "file.upload.request" || request["size"] != float64(len(data)) || request["fileName"] != "notes.bin" {
			t.Errorf("request=%v", request)
			return
		}
		id := request["requestId"].(string)
		var received []byte
		for {
			kind, frame, err := conn.ReadMessage()
			if err != nil {
				t.Error(err)
				return
			}
			if kind != websocket.BinaryMessage || len(frame) < 2 || string(frame[2:2+int(frame[1])]) != id {
				t.Errorf("frame=%v", frame)
				return
			}
			payload := frame[2+int(frame[1]):]
			switch frame[0] {
			case 0x10:
				var metadata map[string]any
				if len(payload) < 2 || int(binary.BigEndian.Uint16(payload)) != len(payload)-2 || json.Unmarshal(payload[2:], &metadata) != nil || metadata["encoding"] != "binary" || metadata["mime"] != "application/octet-stream" {
					t.Errorf("metadata=%s", payload)
					return
				}
			case 0x11:
				if len(payload) > 128*1024 {
					t.Errorf("unbounded chunk=%d", len(payload))
					return
				}
				received = append(received, payload...)
			case 0x12:
				if !bytes.Equal(received, data) {
					t.Errorf("upload bytes differ: got=%d want=%d", len(received), len(data))
					return
				}
				send(t, conn, map[string]any{"type": "session", "message": map[string]any{"type": "file.upload.response", "payload": map[string]any{"requestId": id, "file": map[string]any{"type": "uploaded_file", "id": "file_1", "fileName": "notes.bin", "mimeType": "application/octet-stream", "size": len(data), "path": "/uploads/notes.bin"}, "error": nil}}})
				_, _, _ = conn.ReadMessage()
				return
			default:
				t.Errorf("opcode=%x", frame[0])
				return
			}
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c := &Client{}
	if err := c.Connect(ctx, Target{URL: url}); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	raw, err := c.UploadFile(ctx, "notes.bin", "application/octet-stream", data)
	if err != nil {
		t.Fatal(err)
	}
	var attachment struct{ Type, ID, Path string }
	if json.Unmarshal(raw, &attachment) != nil || attachment.Type != "uploaded_file" || attachment.ID != "file_1" || attachment.Path != "/uploads/notes.bin" {
		t.Fatalf("attachment=%s", raw)
	}
}

func TestUploadFileDisconnectAfterRequestIsUnknown(t *testing.T) {
	url := testServer(t, func(conn *websocket.Conn) { handshake(t, conn); receive(t, conn) })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c := &Client{}
	if err := c.Connect(ctx, Target{URL: url}); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, err := c.UploadFile(ctx, "one.txt", "text/plain", []byte("one"))
	if !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("err=%v", err)
	}
}

func TestUploadFileRejectsMissingAttachmentAcknowledgement(t *testing.T) {
	url := testServer(t, func(conn *websocket.Conn) {
		handshake(t, conn)
		request := receive(t, conn)["message"].(map[string]any)
		for {
			_, frame, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if len(frame) > 0 && frame[0] == 0x12 {
				break
			}
		}
		send(t, conn, map[string]any{"type": "session", "message": map[string]any{"type": "file.upload.response", "payload": map[string]any{"requestId": request["requestId"], "file": nil, "error": nil}}})
		_, _, _ = conn.ReadMessage()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c := &Client{}
	if err := c.Connect(ctx, Target{URL: url}); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.UploadFile(ctx, "empty.txt", "text/plain", nil); err == nil {
		t.Fatal("accepted missing file")
	}
}

func TestCreateTerminalWithCommandPreservesArguments(t *testing.T) {
	url := testServer(t, func(conn *websocket.Conn) {
		handshake(t, conn)
		request := receive(t, conn)["message"].(map[string]any)
		args, ok := request["args"].([]any)
		if request["type"] != "create_terminal_request" || request["workspaceId"] != "workspace" || request["command"] != "codex" || !ok || len(args) != 2 || args[1] != "hello; echo literal" {
			t.Errorf("request=%v", request)
			return
		}
		send(t, conn, map[string]any{"type": "session", "message": map[string]any{"type": "create_terminal_response", "payload": map[string]any{"requestId": request["requestId"], "terminal": map[string]any{"id": "terminal"}, "error": nil}}})
		_, _, _ = conn.ReadMessage()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c := &Client{}
	if err := c.Connect(ctx, Target{URL: url}); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	id, err := c.CreateTerminalWithCommand(ctx, "workspace", "/repo", "Codex", "codex", []string{"--prompt", "hello; echo literal"})
	if err != nil || id != "terminal" {
		t.Fatalf("id=%q err=%v", id, err)
	}
}

func TestCreateTerminalMissingAcknowledgementIsUnknown(t *testing.T) {
	url := testServer(t, func(conn *websocket.Conn) {
		handshake(t, conn)
		request := receive(t, conn)["message"].(map[string]any)
		if _, ok := request["args"]; ok {
			t.Error("nil argv encoded as null")
		}
		send(t, conn, map[string]any{"type": "session", "message": map[string]any{"type": "create_terminal_response", "payload": map[string]any{"requestId": request["requestId"], "terminal": nil, "error": nil}}})
		_, _, _ = conn.ReadMessage()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c := &Client{}
	if err := c.Connect(ctx, Target{URL: url}); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.CreateTerminalWithCommand(ctx, "workspace", "/repo", "", "", nil); !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("err=%v", err)
	}
}
