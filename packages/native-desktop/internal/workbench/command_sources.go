package workbench

import (
	"context"
	"sync"
	"time"
)

type commandDirectory struct {
	Host, Name string
	Workspaces []Workspace
	Agents     []Agent
	Error      string
}

func (a *App) loadCommandDirectory() {
	epoch, gen := a.desk.commandCenter.epoch, a.generation
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		sources, cleanup := a.navigationSources(ctx)
		defer cleanup()
		out := make([]commandDirectory, len(sources))
		var wg sync.WaitGroup
		for i, source := range sources {
			wg.Add(1)
			go func(i int, source navigationSource) {
				defer wg.Done()
				d := commandDirectory{Host: source.ID, Name: source.Name}
				if source.Err != nil {
					d.Error = source.Err.Error()
				} else {
					var err error
					d.Workspaces, d.Agents, err = loadDirectory(ctx, source.Client)
					if err != nil {
						d.Error = err.Error()
					}
				}
				out[i] = d
			}(i, source)
		}
		wg.Wait()
		a.update(gen, func() {
			s := &a.desk.commandCenter
			if s.open && s.epoch == epoch {
				s.directory = out
				s.directoryLoading = false
			}
		})
	}()
}
