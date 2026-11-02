package workbench

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/mermaid"
	"golang.org/x/image/font/gofont/goregular"
)

type diagramRenderer interface {
	Render(context.Context, mermaid.Request) ([]byte, error)
}
type diagramJob struct {
	id      string
	version uint64
	ctx     context.Context
	req     mermaid.Request
}
type diagramState struct {
	source, theme string
	scale         float64
	version       uint64
	cancel        context.CancelFunc
	loading       bool
	err           error
	png           []byte
	image         ui.ImageSource
	fullscreen    bool
}

// Diagrams owns a bounded set of workers and a byte-limited renderer cache.
// All its methods, and callbacks passed to post, must run on the UI thread.
type Diagrams struct {
	renderer diagramRenderer
	post     func(func())
	font     []byte
	fontErr  error
	jobs     chan diagramJob
	done     chan struct{}
	once     sync.Once
	states   map[string]*diagramState
	next     uint64
	closed   bool
}

// NewDiagrams creates a diagram owner. post must schedule its callback on the
// window's UI thread (normally Window.Update). Close it before closing the window.
func NewDiagrams(post func(func())) *Diagrams {
	font, err := diagramFont()
	d := newDiagramManager(mermaid.NewRenderer(2, 32<<20), post, font, 2)
	d.fontErr = err
	return d
}
func newDiagramManager(r diagramRenderer, post func(func()), font []byte, workers int) *Diagrams {
	d := &Diagrams{renderer: r, post: post, font: font, jobs: make(chan diagramJob, 64), done: make(chan struct{}), states: make(map[string]*diagramState)}
	for range workers {
		go d.worker()
	}
	return d
}

func (d *Diagrams) worker() {
	for {
		select {
		case <-d.done:
			return
		case j := <-d.jobs:
			var data []byte
			var err error
			if err = j.ctx.Err(); err == nil {
				data, err = d.renderer.Render(j.ctx, j.req)
			}
			select {
			case <-d.done:
				return
			default:
			}
			d.post(func() { d.complete(j, data, err) })
		}
	}
}
func (d *Diagrams) complete(j diagramJob, data []byte, err error) {
	if d.closed {
		return
	}
	s := d.states[j.id]
	if s == nil || s.version != j.version {
		return
	}
	s.loading = false
	s.cancel = nil
	if errors.Is(err, mermaid.ErrMissingGlyph) {
		err = fmt.Errorf("%w; set PASEO_CJK_FONT to an installed CJK font", err)
	}
	s.err = err
	if err == nil {
		s.png = data
		s.image = nil
	}
}
func (d *Diagrams) request(id, source, theme string, scale float64) *diagramState {
	s := d.states[id]
	if s == nil {
		s = &diagramState{}
		d.states[id] = s
	} else if s.source == source && s.theme == theme && s.scale == scale {
		return s
	}
	if s.cancel != nil {
		s.cancel()
	}
	s.source, s.theme, s.scale = source, theme, scale
	d.start(id, s)
	return s
}
func (d *Diagrams) start(id string, s *diagramState) {
	d.next++
	s.version = d.next
	s.err = nil
	if d.closed {
		s.loading = false
		return
	}
	if d.fontErr != nil {
		s.loading = false
		s.err = d.fontErr
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	j := diagramJob{id: id, version: s.version, ctx: ctx, req: mermaid.Request{Source: s.source, Theme: s.theme, Font: d.font, Scale: s.scale}}
	select {
	case d.jobs <- j:
		s.loading = true
	default:
		cancel()
		s.cancel = nil
		s.loading = false
		s.err = fmt.Errorf("diagram queue is full; retry when other diagrams finish")
	}
}
func (d *Diagrams) retry(id string) {
	if s := d.states[id]; s != nil && !s.loading {
		d.start(id, s)
	}
}

// Reset releases a page's image references and cancels its outstanding work.
// Call it when changing the selected agent or disconnecting.
func (d *Diagrams) Reset() {
	for _, s := range d.states {
		if s.cancel != nil {
			s.cancel()
		}
	}
	d.states = make(map[string]*diagramState)
}

// Close cancels outstanding work and stops workers; it is safe to call twice.
func (d *Diagrams) Close() {
	if d.closed {
		return
	}
	d.closed = true
	d.Reset()
	d.once.Do(func() { close(d.done) })
}

// Hook returns the Mermaid code-block hook for a stable timeline row identity.
// Install it in Options.Code["mermaid"] before rendering that row.
func (d *Diagrams) Hook(rowID string) func(*ui.Context, *markdown.CodeBlockView) {
	return func(c *ui.Context, b *markdown.CodeBlockView) {
		if b.Open {
			markdown.DefaultCodeBlock(c, b)
			return
		}
		id := fmt.Sprintf("%s/%d", rowID, b.Index)
		theme := "default"
		if c.Theme().Dark {
			theme = "dark"
		}
		s := d.states[id]
		scale := 1.0
		if s != nil && s.scale > 0 {
			scale = s.scale
		}
		s = d.request(id, b.Code(), theme, scale)
		d.view(c, b, id, s)
	}
}
func (d *Diagrams) view(c *ui.Context, b *markdown.CodeBlockView, id string, s *diagramState) {
	ui.Column(c).Gap(6).AlignSelf(ui.Stretch).Children(func() {
		ui.Row(c).Gap(6).Children(func() {
			ui.Text(c, "Mermaid").Bold()
			if s.loading {
				ui.Text(c, "Rendering…")
			}
			if ui.Button(c.Key("zoom-out"), "−").Label("Zoom out diagram").Clicked() {
				d.request(id, s.source, s.theme, max(0.5, s.scale/1.5))
			}
			if ui.Button(c.Key("zoom-in"), "+").Label("Zoom in diagram").Clicked() {
				d.request(id, s.source, s.theme, min(4.0, s.scale*1.5))
			}
			if ui.Button(c.Key("fullscreen"), "Fullscreen").Clicked() {
				s.fullscreen = true
			}
			b.CopyButton(c)
		})
		if s.err != nil {
			ui.Text(c, "Diagram error: "+s.err.Error())
			if ui.Button(c.Key("retry"), "Retry").Clicked() {
				d.retry(id)
			}
		}
		d.picture(c, s, 240)
		ui.Modal(c.Key("fullscreen-dialog"), &s.fullscreen, func() {
			w, h := c.Size()
			ui.Column(c).Width(w * 0.9).Height(h * 0.9).Gap(8).Children(func() {
				ui.Row(c).Gap(8).Children(func() {
					if ui.Button(c.Key("zoom-out"), "−").Label("Zoom out fullscreen").Clicked() {
						d.request(id, s.source, s.theme, max(0.5, s.scale/1.5))
					}
					if ui.Button(c.Key("zoom-in"), "+").Label("Zoom in fullscreen").Clicked() {
						d.request(id, s.source, s.theme, min(4.0, s.scale*1.5))
					}
					if ui.Button(c.Key("close"), "Close").Clicked() {
						s.fullscreen = false
					}
				})
				d.picture(c, s, h*0.75)
			})
		})
	})
}
func (d *Diagrams) picture(c *ui.Context, s *diagramState, height float32) {
	if s.image == nil && len(s.png) != 0 {
		img, err := ui.DecodeImage(s.png)
		if err != nil {
			s.err = err
			s.png = nil
		} else {
			s.image = img
		}
	}
	ui.ScrollBoth(c.Key("diagram-image")).Height(height).AlignSelf(ui.Stretch).Children(func() {
		if s.image != nil {
			w, h := ui.ImageSize(s.image)
			ui.Image(c, s.image).Size(w, h).Shrink(0).Label("Mermaid diagram")
		} else {
			ui.Text(c, "Diagram preview")
		}
	})
}

func diagramFont() ([]byte, error) {
	if path := strings.TrimSpace(os.Getenv("PASEO_CJK_FONT")); path != "" {
		font, err := readDiagramFont(path)
		if err != nil {
			return nil, fmt.Errorf("PASEO_CJK_FONT: %w", err)
		}
		return font, nil
	}
	patterns := []string{
		"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc", "/usr/share/fonts/noto-cjk/NotoSansCJK-Regular.ttc", "/usr/share/fonts/TTF/NotoSansCJK-Regular.ttc",
		"/usr/share/fonts/**/*NotoSansCJK*Regular*.ttc", "/usr/share/fonts/**/*NotoSansCJK*Regular*.otf",
		"/System/Library/Fonts/PingFang.ttc", "/System/Library/Fonts/Hiragino Sans GB.ttc", "C:/Windows/Fonts/msyh.ttc", "C:/Windows/Fonts/simsun.ttc",
	}
	for _, pattern := range patterns {
		paths, _ := filepath.Glob(pattern)
		for _, path := range paths {
			if b, err := readDiagramFont(path); err == nil {
				return b, nil
			}
		}
	}
	return goregular.TTF, nil
}
func readDiagramFont(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 64<<20 {
		return nil, fmt.Errorf("font must be a regular file of at most 64 MiB")
	}
	b := make([]byte, info.Size())
	if _, err := io.ReadFull(f, b); err != nil {
		return nil, err
	}
	return b, nil
}
