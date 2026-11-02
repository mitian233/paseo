package mermaid

import (
	"bytes"
	"encoding/xml"
	upstream "github.com/zkrebbekx/go-mermaid"
	"io"
	"strings"
	"testing"
)

var requiredMermaidFixtures = []struct {
	name, source string
	labels       []string
}{
	{"sequence participant groups", "sequenceDiagram\nbox rgb(240,240,240) Backend\n participant API as Gateway\n participant DB as Storage\nend\nAPI->>DB: Query", []string{"Backend", "Gateway", "Storage", "Query"}},
	{"C4 boundaries and descriptions", "C4Container\nSystem_Boundary(platform, \"Platform\") {\nContainer(api, \"Gateway\", \"Go\", \"Handles requests\")\n}", []string{"Platform", "Gateway", "Handles requests"}},
	{"Gantt axis formatting", "gantt\ndateFormat YYYY-MM-DD\naxisFormat %d/%m\nexcludes weekends\nsection Work\nTask :2026-11-06, 2d", []string{"06/11", "Task"}},
	{"flowchart modern shape", "flowchart LR\n A@{shape: cloud, label: \"Host\"} --> B[Agent]", []string{"Host", "Agent"}},
	{"class diagram", "classDiagram\n class Host\n class Agent\n Host --> Agent : manages", []string{"Host", "Agent", "manages"}},
	{"state diagram", "stateDiagram-v2\n [*] --> Connected\n Connected --> Disconnected : lost", []string{"Connected", "Disconnected", "lost"}},
	{"entity relationship", "erDiagram\n HOST ||--o{ AGENT : owns", []string{"HOST", "AGENT", "owns"}},
	{"pie", "pie title Usage\n \"Active\" : 60\n \"Idle\" : 40", []string{"Usage", "Active", "Idle"}},
	{"journey", "journey\n title Workflow\n section Work\n Connect: 5: User", []string{"Workflow", "Connect"}},
	{"quadrant", "quadrantChart\n title Priorities\n x-axis Low --> High\n y-axis Slow --> Fast\n Task: [0.7, 0.8]", []string{"Priorities", "Task"}},
	{"git graph", "gitGraph\n commit id: \"Initial\"\n branch feature\n checkout feature\n commit id: \"Work\"", []string{"Initial", "Work"}},
	{"timeline", "timeline\n title Releases\n 2026 : Native", []string{"Releases", "Native"}},
	{"mindmap", "mindmap\n root((Paseo))\n  Client\n  Daemon", []string{"Paseo", "Client", "Daemon"}},
	{"requirement", "requirementDiagram\n requirement logging {\n id: 1\n text: keep logs\n risk: low\n verifymethod: test\n }", []string{"logging", "keep logs"}},
	{"sankey", "sankey-beta\nSource,Target,10", []string{"Source", "Target"}},
	{"XY chart", "xychart-beta\n title Load\n x-axis [Mon, Tue]\n y-axis \"Calls\" 0 --> 10\n bar [2, 8]", []string{"Load", "Calls"}},
	{"block connectors", "block-beta\n columns 2\n a[\"Client\"] b[\"Server\"]\n a -- \"Payload\" --> b", []string{"Client", "Server", "Payload"}},
	{"kanban", "kanban\n todo[Todo]\n  task1[Connect]", []string{"Todo", "Connect"}},
	{"packet", "packet-beta\n 0-7: \"Header\"\n 8-15: \"Data\"", []string{"Header", "Data"}},
	{"radar", "radar-beta\n title Scores\n axis speed[\"Speed\"], quality[\"Quality\"]\n curve native[\"Native\"]{80, 90}\n max 100", []string{"Scores", "Speed", "Quality", "Native"}},
	{"architecture", "architecture-beta\n group api(cloud)[API]\n service db(database)[Database] in api\n service server(server)[Server] in api\n db:R --> L:server", []string{"API", "Database", "Server"}},
	{"treemap", "---\nconfig:\n treemap:\n  valueFormat: '$,.2f'\n---\ntreemap-beta\n\"Budget\"\n \"Operations\": 1234.5", []string{"Budget", "Operations", "$1,234.50"}},
}

func TestRequiredMermaidExtensionLabels(t *testing.T) {
	for _, tc := range requiredMermaidFixtures {
		t.Run(tc.name, func(t *testing.T) {
			svg, err := upstream.Render(tc.source)
			if err != nil {
				t.Fatal(err)
			}
			visible := visibleDiagramText(t, svg)
			for _, label := range tc.labels {
				if !strings.Contains(visible, label) {
					t.Errorf("render lost visible diagram label %q", label)
				}
			}
		})
	}
}

func visibleDiagramText(t *testing.T, svg []byte) string {
	t.Helper()
	decoder := xml.NewDecoder(bytes.NewReader(svg))
	depth := 0
	var text strings.Builder
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch token := token.(type) {
		case xml.StartElement:
			if token.Name.Local == "text" || token.Name.Local == "tspan" {
				depth++
			}
		case xml.EndElement:
			if token.Name.Local == "text" || token.Name.Local == "tspan" {
				depth--
			}
		case xml.CharData:
			if depth > 0 {
				text.Write(token)
				text.WriteByte(' ')
			}
		}
	}
	return strings.Join(strings.Fields(text.String()), " ")
}
