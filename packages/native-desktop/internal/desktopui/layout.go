package desktopui

// Layout describes the currently visible widths; it never changes saved preferences.
type Layout struct {
	NavVisible, ExplorerVisible bool
	NavWidth, ExplorerWidth     float32
}

func (s *ShellState) Layout(width float32) Layout {
	nav := s.NavWidth
	if nav <= 0 {
		nav = 320
	}
	nav = max(nav, 200)
	explorer := s.ExplorerWidth
	if explorer <= 0 {
		explorer = 280
	}
	explorer = max(explorer, 240)
	out := Layout{}
	if s.ExplorerOpen && width >= 600 {
		out.ExplorerVisible = true
		out.ExplorerWidth = min(explorer, max(240, width-360))
	}
	reserve := float32(400)
	if out.ExplorerVisible {
		reserve = 360 + out.ExplorerWidth
	}
	if !s.NavHidden && width >= reserve+200 {
		out.NavVisible = true
		out.NavWidth = min(nav, width-reserve)
	}
	return out
}
