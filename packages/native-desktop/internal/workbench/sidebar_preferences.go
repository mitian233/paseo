package workbench

import (
	"github.com/getpaseo/paseo/packages/native-desktop/internal/desktopui"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/workbenchstate"
)

func (a *App) saveSidebarNavigation(items []desktopui.NavPreference) {
	a.desk.prefs.SidebarNav = nil
	for _, item := range items {
		a.desk.prefs.SidebarNav = append(a.desk.prefs.SidebarNav, workbenchstate.SidebarNavPreference{ID: item.ID, Hidden: item.Hidden})
	}
	a.persistSoon()
}
