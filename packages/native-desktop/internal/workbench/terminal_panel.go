package workbench

import (
	"context"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/client"
)

type terminalPanel struct {
	id       string
	terminal *terminal.Terminal
}

func newTerminalPanel(ctx context.Context, c *client.Client, workspaceID, cwd, terminalID string, notify func()) (*terminalPanel, error) {
	var err error
	if terminalID == "" {
		terminalID, err = c.CreateTerminal(ctx, workspaceID, cwd)
		if err != nil {
			return nil, err
		}
	}
	stream, err := c.SubscribeTerminal(ctx, terminalID)
	if err != nil {
		return nil, err
	}
	ready := make(chan struct{})
	var term *terminal.Terminal
	stream.OnReplaySize(func(cols, rows int) {
		<-ready
		if term != nil {
			term.Resize(cols, rows)
		}
	})
	term, err = terminal.New(terminal.Options{Conn: stream, OnExit: func(int) {
		if notify != nil {
			notify()
		}
	}})
	close(ready)
	if err != nil {
		_ = stream.Close()
		return nil, err
	}
	return &terminalPanel{id: terminalID, terminal: term}, nil
}
func (p *terminalPanel) ID() string         { return p.id }
func (p *terminalPanel) View(c *ui.Context) { terminal.View(c, p.terminal).Fill() }
func (p *terminalPanel) Close() error       { return p.terminal.Close() }
