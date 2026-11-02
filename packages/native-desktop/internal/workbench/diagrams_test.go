package workbench

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/mermaid"
)

type diagramAnswer struct {
	source string
	done   chan struct{}
}
type controlledDiagrams struct{ calls chan diagramAnswer }

func (r *controlledDiagrams) Render(ctx context.Context, req mermaid.Request) ([]byte, error) {
	a := diagramAnswer{req.Source, make(chan struct{})}
	r.calls <- a
	select {
	case <-a.done:
		return []byte(req.Source), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func diagramCall(t *testing.T, c <-chan diagramAnswer) diagramAnswer {
	t.Helper()
	select {
	case a := <-c:
		return a
	case <-time.After(time.Second):
		t.Fatal("render did not start")
		return diagramAnswer{}
	}
}
func diagramPost(t *testing.T, c <-chan func()) {
	t.Helper()
	select {
	case f := <-c:
		f()
	case <-time.After(time.Second):
		t.Fatal("result not posted")
	}
}

func TestDiagramChangedSourceCancelsStaleResult(t *testing.T) {
	r := &controlledDiagrams{make(chan diagramAnswer, 4)}
	posts := make(chan func(), 4)
	d := newDiagramManager(r, func(f func()) { posts <- f }, nil, 1)
	defer d.Close()
	d.request("row/1", "old", "light", 1)
	first := diagramCall(t, r.calls)
	d.request("row/1", "new", "light", 1)
	diagramPost(t, posts)
	second := diagramCall(t, r.calls)
	if first.source != "old" || second.source != "new" {
		t.Fatal("wrong source delivered")
	}
	close(second.done)
	diagramPost(t, posts)
	s := d.states["row/1"]
	if s.loading || s.err != nil || string(s.png) != "new" {
		t.Fatalf("latest result not retained: %+v", s)
	}
}

func TestDiagramResetCancelsAndIgnoresCompletion(t *testing.T) {
	r := &controlledDiagrams{make(chan diagramAnswer, 4)}
	posts := make(chan func(), 4)
	d := newDiagramManager(r, func(f func()) { posts <- f }, nil, 1)
	defer d.Close()
	d.request("row/1", "old", "light", 1)
	diagramCall(t, r.calls)
	d.Reset()
	diagramPost(t, posts)
	if len(d.states) != 0 {
		t.Fatal("released diagram was restored")
	}
}

func TestDiagramRepeatedRequestKeepsResultAndRetryIsExplicit(t *testing.T) {
	r := &errorDiagramRenderer{}
	posts := make(chan func(), 4)
	d := newDiagramManager(r, func(f func()) { posts <- f }, nil, 1)
	defer d.Close()
	d.request("row", "bad", "light", 1)
	diagramPost(t, posts)
	if d.states["row"].err == nil {
		t.Fatal("missing render error")
	}
	d.request("row", "bad", "light", 1)
	if d.states["row"].loading {
		t.Fatal("repeated frame retried error")
	}
	d.retry("row")
	diagramPost(t, posts)
	if d.states["row"].err == nil {
		t.Fatal("retry did not complete")
	}
}

type errorDiagramRenderer struct{}

func (*errorDiagramRenderer) Render(context.Context, mermaid.Request) ([]byte, error) {
	return nil, errors.New("invalid diagram")
}

func TestDiagramReusesSuccessfulResultAndPreservesItWhileRendering(t *testing.T) {
	r := &controlledDiagrams{make(chan diagramAnswer, 4)}
	posts := make(chan func(), 4)
	d := newDiagramManager(r, func(f func()) { posts <- f }, nil, 1)
	defer d.Close()
	d.request("row", "first", "default", 1)
	first := diagramCall(t, r.calls)
	close(first.done)
	diagramPost(t, posts)
	s := d.request("row", "first", "default", 1)
	if s.loading || string(s.png) != "first" {
		t.Fatal("unchanged request did not reuse its result")
	}
	s = d.request("row", "second", "default", 1)
	if !s.loading || string(s.png) != "first" {
		t.Fatal("previous result was lost while rendering")
	}
	second := diagramCall(t, r.calls)
	close(second.done)
	diagramPost(t, posts)
	if string(s.png) != "second" {
		t.Fatal("new result did not replace previous result")
	}
	d.request("row", "second", "dark", 2)
	third := diagramCall(t, r.calls)
	close(third.done)
	diagramPost(t, posts)
	if s.theme != "dark" || s.scale != 2 || s.loading {
		t.Fatal("changed settings did not render")
	}
}

func TestDiagramFullscreenZoomControls(t *testing.T) {
	posts := make(chan func(), 8)
	d := newDiagramManager(&errorDiagramRenderer{}, func(f func()) { posts <- f }, nil, 1)
	defer d.Close()
	options := markdown.Options{Code: map[string]func(*ui.Context, *markdown.CodeBlockView){"mermaid": d.Hook("row")}}
	tt := ui.NewTester(func(c *ui.Context) { markdown.View(c, "```mermaid\nflowchart LR\n A-->B\n```", &options) }, 1100, 760)
	if err := tt.Click("Fullscreen"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Zoom in fullscreen"); err != nil {
		t.Fatal(err)
	}
	for _, state := range d.states {
		if !state.fullscreen || state.scale != 1.5 {
			t.Fatalf("fullscreen zoom: open=%v scale=%v", state.fullscreen, state.scale)
		}
	}
	if err := tt.Click("Zoom out fullscreen"); err != nil {
		t.Fatal(err)
	}
	for _, state := range d.states {
		if state.scale != 1 {
			t.Fatalf("zoom out: scale=%v", state.scale)
		}
	}
}
