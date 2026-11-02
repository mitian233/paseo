package main

import (
	"flag"
	"log"
	"os"

	"github.com/egoist/mygo"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/workbench"
)

func main() {
	url := flag.String("url", os.Getenv("PASEO_DAEMON_URL"), "daemon websocket URL")
	flag.Parse()
	app := workbench.New(*url)
	mygo.App.SetName("Paseo")
	mygo.App.WhenReady(app.Open)
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
