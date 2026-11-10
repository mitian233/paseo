package desktopui

import (
	_ "embed"

	"github.com/egoist/mygo/ui"
)

//go:embed paseo-logo.svg
var paseoLogoData []byte

var paseoLogo = ui.MustParseSVG(paseoLogoData)

//go:embed github.svg
var homeGithubData []byte

//go:embed discord.svg
var homeDiscordData []byte

var homeCommunityLinks = [...]struct {
	label, url string
	icon       *ui.SVG
}{
	{"Star", "https://github.com/getpaseo/paseo", ui.MustParseSVG(homeGithubData)},
	{"Sponsor", "https://github.com/sponsors/boudra", ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" d="M20.84 4.61a5.5 5.5 0 0 0-7.78 0L12 5.67l-1.06-1.06a5.5 5.5 0 0 0-7.78 7.78L12 21.23l8.84-8.84a5.5 5.5 0 0 0 0-7.78z"/></svg>`))},
	{"Community", "https://discord.gg/jz8T2uahpH", ui.MustParseSVG(homeDiscordData)},
}

type HomeActions struct {
	Menu, Reconnect                                       func()
	AddProject, ImportSession, SetupProviders, PairDevice func()
}

var homeTiles = [...]struct {
	title, description string
	icon               *ui.SVG
}{
	{"Add a project", "Open a folder on your machine", folderOpenIcon},
	{"Import session", "Open a Claude Code, Codex or other session you started in a terminal", inboxIcon},
	{"Setup providers", "Configure Claude Code, Codex, and more", plugIcon},
	{"Pair device", "Connect your phone to this daemon", phoneIcon},
}

// Home uses the measured desktop OpenProjectScreen geometry. Pairing is only
// present when the current host advertises the corresponding capability.
func Home(c *ui.Context, props ShellProps, actions HomeActions) {
	theme, colors := Theme(props.Theme, props.SystemDark)
	c.SetTheme(theme)
	_, height := c.Size()
	ui.Column(c).Fill().Background(colors.Background).Children(func() {
		ui.Row(c).Height(36).Shrink(0).Padding(0, 12).Children(func() {
			if iconButton(c.Key("home-menu"), "Menu", explorerIcon, colors).Size(26, 26).Clicked() && actions.Menu != nil {
				actions.Menu()
			}
		})
		ui.Scroll(c).Grow(1).Shrink(1).Children(func() {
			ui.Column(c).FillWidth().MinHeight(max(0, height-36)).Children(func() {
				ui.Column(c).Grow(1).Shrink(0).Padding(24).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
					ui.Icon(c, paseoLogo).Size(52, 52).TextColor(colors.Foreground).Margin(0, 0, 32, 0)
					if props.Status != "" {
						ui.Text(c, props.Status).MaxWidth(452).TextColor(colors.Muted)
					}
					if actions.Reconnect != nil {
						if ui.Button(c, "Reconnect").Clicked() {
							actions.Reconnect()
						}
					}
					callbacks := [...]func(){actions.AddProject, actions.ImportSession, actions.SetupProviders, actions.PairDevice}
					ui.Row(c).WidthPercent(100).MaxWidth(452).Wrap().AlignItems(ui.Stretch).Gap(12).Margin(48, 0, 0, 0).Children(func() {
						for i, tile := range homeTiles {
							action := callbacks[i]
							if action == nil {
								continue
							}
							width := float32(220)
							if props.Width < 500 {
								width = max(0, props.Width-48)
							}
							button := ui.ButtonBase(c.Key("home-tile:"+tile.title)).Label(tile.title).Width(width).MinHeight(132).Padding(16).Radius(12).Justify(ui.Start).AlignItems(ui.Start).Background(colors.Surface).Border(1, colors.Border)
							if button.Hovered() {
								button.Background(colors.Selected).BorderColor(theme.Accent)
							}
							button.Children(func() {
								ui.Column(c).FillWidth().Gap(12).Children(func() {
									color := colors.Muted
									if i == 0 {
										color = theme.Accent
									}
									ui.Icon(c, tile.icon).Size(20, 20).TextColor(color)
									ui.Column(c).FillWidth().Gap(4).Children(func() {
										ui.Text(c, tile.title).FontSize(14).LineHeight(19.0 / 14).TextColor(colors.Foreground)
										ui.Text(c, tile.description).FontSize(14).LineHeight(18.0 / 14).TextColor(colors.Muted)
									})
								})
							})
							if button.Clicked() {
								action()
							}
						}
					})
				})
				ui.Row(c).Height(76).Shrink(0).Padding(0, 0, 44, 0).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
					for _, link := range homeCommunityLinks {
						ui.Row(c).Height(32).Padding(0, 16).Gap(8).AlignItems(ui.Center).Children(func() {
							ui.Icon(c, link.icon).Size(14, 14).TextColor(colors.Muted)
							ui.Link(c, link.label, link.url).TextColor(colors.Muted)
						})
					}
				})
			})
		})
	})
}
