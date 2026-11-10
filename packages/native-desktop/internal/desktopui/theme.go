package desktopui

import "github.com/egoist/mygo/ui"

type Tokens struct{ Background, Sidebar, Surface, Selected, Foreground, Muted, Border ui.Color }

func Theme(mode string, systemDark bool) (*ui.Theme, Tokens) {
	dark := mode == "dark" || (mode != "light" && systemDark)
	var t *ui.Theme
	var colors Tokens
	if dark {
		t = ui.DarkTheme()
		colors = Tokens{ui.Hex("#1E2120"), ui.Hex("#141716"), ui.Hex("#181B1A"), ui.Hex("#272A29"), ui.Hex("#f4f4f5"), ui.Hex("#A1A5A4"), ui.Hex("#252B2A")}
	} else {
		t = ui.LightTheme()
		colors = Tokens{ui.Hex("#ffffff"), ui.Hex("#f4f4f5"), ui.Hex("#fafafa"), ui.Hex("#d4d4d8"), ui.Hex("#1a1a1e"), ui.Hex("#71717a"), ui.Hex("#e4e4e7")}
	}
	t.Background, t.Surface, t.Border = colors.Background, colors.Surface, colors.Border
	t.Text, t.TextMuted = colors.Foreground, colors.Muted
	t.Accent = ui.Hex("#20744A")
	t.FontSize = 14
	return t, colors
}
