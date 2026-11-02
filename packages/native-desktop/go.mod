module github.com/getpaseo/paseo/packages/native-desktop

go 1.27.3

require (
	github.com/egoist/mygo v0.7.4-0.20261030064541-2b66322aa335
	github.com/gorilla/websocket v1.5.3
	github.com/srwiley/oksvg v0.0.0-20221011165216-be6e8873101c
	github.com/srwiley/rasterx v0.0.0-20220730225603-2ab79fcdd4ef
	github.com/zkrebbekx/go-mermaid v0.1.6
	golang.org/x/image v0.46.0
)

require (
	github.com/ebitengine/purego v0.11.1 // indirect
	github.com/go-text/typesetting v0.3.5 // indirect
	golang.org/x/net v0.59.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/zkrebbekx/go-mermaid => ./third_party/go-mermaid
