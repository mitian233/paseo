package client

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// UploadFile uses the same connection and correlated request as the file frames.
// The returned value is an UploadedFileAttachment, ready for an agent request.
func (c *Client) UploadFile(ctx context.Context, name, mime string, data []byte) (json.RawMessage, error) {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(mime) == "" {
		return nil, errors.New("file name and MIME type are required")
	}
	id, err := randomID()
	if err != nil {
		return nil, err
	}
	modified := time.Now().UTC().Format(time.RFC3339Nano)
	pending := &pendingCall{ch: make(chan result, 1)}
	c.mu.Lock()
	conn := c.conn
	if conn == nil {
		c.mu.Unlock()
		return nil, ErrDisconnected
	}
	c.pending[id] = pending
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		if c.conn == conn {
			delete(c.pending, id)
		}
		c.mu.Unlock()
	}()
	fields := map[string]any{"type": "file.upload.request", "requestId": id, "fileName": name, "mimeType": mime, "size": len(data), "modifiedAt": modified}
	started, err := c.write(ctx, conn, map[string]any{"type": "session", "message": fields}, pending)
	if err != nil {
		if !started {
			return nil, err
		}
		c.disconnect(conn, err)
		return nil, fmt.Errorf("%w: %v", ErrOutcomeUnknown, err)
	}
	metadata, _ := json.Marshal(map[string]any{"mime": mime, "size": len(data), "encoding": "binary", "modifiedAt": modified, "fileName": name})
	if len(metadata) > 65535 {
		return nil, fmt.Errorf("%w: file metadata too long", ErrOutcomeUnknown)
	}
	begin := make([]byte, 4+len(id)+len(metadata))
	begin[0], begin[1] = 0x10, byte(len(id))
	copy(begin[2:], id)
	binary.BigEndian.PutUint16(begin[2+len(id):], uint16(len(metadata)))
	copy(begin[4+len(id):], metadata)
	writeFrame := func(frame []byte) (*result, error) {
		c.mu.Lock()
		writer := c.writer
		c.mu.Unlock()
		select {
		case writer <- struct{}{}:
		case r := <-pending.ch:
			return &r, nil
		case <-ctx.Done():
			return nil, fmt.Errorf("%w: %v", ErrOutcomeUnknown, ctx.Err())
		}
		defer func() { <-writer }()
		select {
		case r := <-pending.ch:
			return &r, nil
		default:
		}
		c.mu.Lock()
		valid := c.conn == conn
		c.mu.Unlock()
		if !valid {
			return nil, ErrOutcomeUnknown
		}
		deadline := time.Now().Add(30 * time.Second)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		_ = conn.SetWriteDeadline(deadline)
		stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
		err := conn.WriteMessage(websocket.BinaryMessage, frame)
		if !stop() && ctx.Err() != nil {
			err = ctx.Err()
		}
		if err != nil {
			c.disconnect(conn, err)
			return nil, fmt.Errorf("%w: %v", ErrOutcomeUnknown, err)
		}
		return nil, nil
	}
	var early *result
	if early, err = writeFrame(begin); err != nil {
		return nil, err
	}
	// Reuse one bounded frame buffer; WriteMessage consumes it synchronously.
	frame := make([]byte, 2+len(id)+min(len(data), 128*1024))
	frame[1] = byte(len(id))
	copy(frame[2:], id)
	for offset := 0; early == nil && offset < len(data); {
		n := min(len(data)-offset, 128*1024)
		frame[0] = 0x11
		copy(frame[2+len(id):], data[offset:offset+n])
		early, err = writeFrame(frame[:2+len(id)+n])
		if err != nil {
			return nil, err
		}
		offset += n
	}
	if early == nil {
		frame[0] = 0x12
		early, err = writeFrame(frame[:2+len(id)])
		if err != nil {
			return nil, err
		}
	}
	if early == nil {
		select {
		case r := <-pending.ch:
			early = &r
		case <-ctx.Done():
			return nil, fmt.Errorf("%w: %v", ErrOutcomeUnknown, ctx.Err())
		}
	}
	if early.err != nil {
		return nil, early.err
	}
	var ack struct {
		File  json.RawMessage `json:"file"`
		Error *string         `json:"error"`
	}
	if err := json.Unmarshal(early.payload, &ack); err != nil {
		return nil, fmt.Errorf("%w: invalid upload response", ErrOutcomeUnknown)
	}
	if ack.Error != nil && *ack.Error != "" {
		return nil, errors.New(*ack.Error)
	}
	var attachment struct{ Type, ID, Path string }
	if json.Unmarshal(ack.File, &attachment) != nil || attachment.Type != "uploaded_file" || attachment.ID == "" || attachment.Path == "" {
		return nil, fmt.Errorf("%w: missing uploaded file", ErrOutcomeUnknown)
	}
	return ack.File, nil
}

// CreateTerminalWithCommand starts an existing workspace terminal with argv
// supplied as distinct entries; command text is never interpreted by a shell.
func (c *Client) CreateTerminalWithCommand(ctx context.Context, workspaceID, cwd, name, command string, args []string) (string, error) {
	fields := map[string]any{"workspaceId": workspaceID, "cwd": cwd}
	if command != "" {
		fields["command"] = command
	}
	if name != "" {
		fields["name"] = name
	}
	if args != nil {
		fields["args"] = args
	}
	body, _ := json.Marshal(fields)
	raw, err := c.Call(ctx, "create_terminal_request", body)
	if err != nil {
		return "", err
	}
	var ack struct {
		Terminal *struct {
			ID string `json:"id"`
		} `json:"terminal"`
		Error *string `json:"error"`
	}
	if err := json.Unmarshal(raw, &ack); err != nil {
		return "", fmt.Errorf("%w: invalid terminal response", ErrOutcomeUnknown)
	}
	if ack.Error != nil && *ack.Error != "" {
		return "", errors.New(*ack.Error)
	}
	if ack.Terminal == nil || ack.Terminal.ID == "" {
		return "", fmt.Errorf("%w: daemon returned no terminal", ErrOutcomeUnknown)
	}
	return ack.Terminal.ID, nil
}
