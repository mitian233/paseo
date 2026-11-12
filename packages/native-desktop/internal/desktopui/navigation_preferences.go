package desktopui

// OrderedNavigation preserves explicit user order and appends newly introduced entries.
func OrderedNavigation(prefs []NavPreference) []NavPreference {
	defaults := []string{"new-workspace", "history", "search", "schedules"}
	seen := map[string]bool{}
	out := make([]NavPreference, 0, 4)
	for _, pref := range prefs {
		valid := false
		for _, id := range defaults {
			if id == pref.ID {
				valid = true
				break
			}
		}
		if valid && !seen[pref.ID] {
			out = append(out, pref)
			seen[pref.ID] = true
		}
	}
	for _, id := range defaults {
		if !seen[id] {
			out = append(out, NavPreference{ID: id})
		}
	}
	return out
}
