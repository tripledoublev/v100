package main

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tripledoublev/v100/internal/core"
	"github.com/tripledoublev/v100/internal/ui"
)

func graphCmd() *cobra.Command {
	var outputPath string
	cmd := &cobra.Command{
		Use:   "graph <run_id>",
		Short: "Render a run trace as an interactive DAG explorer",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			runDir, err := findRunDir(args[0])
			if err != nil {
				return err
			}
			events, err := core.ReadAll(filepath.Join(runDir, "trace.jsonl"))
			if err != nil {
				return err
			}
			if outputPath == "" {
				outputPath = filepath.Join(runDir, "trace-dag.html")
			}
			doc, err := renderTraceDAGHTML(filepath.Base(runDir), runDir, events)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(outputPath, []byte(doc), 0o644); err != nil {
				return err
			}
			fmt.Println(ui.Info("wrote trace DAG: " + outputPath))
			return nil
		},
	}
	cmd.Flags().StringVarP(&outputPath, "output", "o", "", "HTML output path (default: runs/<id>/trace-dag.html)")
	return cmd
}

type dagNode struct {
	ID             string `json:"id"`
	Label          string `json:"label"`
	Type           string `json:"type"`
	StepID         string `json:"step_id,omitempty"`
	EventID        string `json:"event_id,omitempty"`
	SnapshotID     string `json:"snapshot_id,omitempty"`
	WorkspaceState string `json:"workspace_state,omitempty"`
	Payload        string `json:"payload"`
	X              int    `json:"x"`
	Y              int    `json:"y"`
}

type dagEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}

func renderTraceDAGHTML(runID, runDir string, events []core.Event) (string, error) {
	nodes, edges := buildTraceDAG(runDir, events)
	payload, err := json.Marshal(struct {
		RunID string    `json:"run_id"`
		Nodes []dagNode `json:"nodes"`
		Edges []dagEdge `json:"edges"`
	}{RunID: runID, Nodes: nodes, Edges: edges})
	if err != nil {
		return "", err
	}
	return traceDAGHTML(runID, string(payload)), nil
}

func buildTraceDAG(runDir string, events []core.Event) ([]dagNode, []dagEdge) {
	nodes := make([]dagNode, 0, len(events))
	edges := make([]dagEdge, 0, maxInt(0, len(events)-1))
	latestSnapshotID := ""
	snapshotNodes := make(map[string]string)
	for i, ev := range events {
		eventID := ev.EventID
		if eventID == "" {
			eventID = fmt.Sprintf("event-%d", i+1)
		}
		nodeID := fmt.Sprintf("n%d", i)
		snapshotID := snapshotIDForEvent(ev)
		if ev.Type == core.EventSandboxSnapshot && snapshotID != "" {
			latestSnapshotID = snapshotID
			snapshotNodes[snapshotID] = nodeID
		}
		stateSnapshot := latestSnapshotID
		if ev.Type == core.EventSandboxRestore && snapshotID != "" {
			stateSnapshot = snapshotID
			latestSnapshotID = snapshotID
		}
		nodes = append(nodes, dagNode{
			ID:             nodeID,
			Label:          dagNodeLabel(ev, i),
			Type:           string(ev.Type),
			StepID:         ev.StepID,
			EventID:        eventID,
			SnapshotID:     snapshotID,
			WorkspaceState: snapshotTreeSummary(runDir, stateSnapshot),
			Payload:        prettyPayload(ev.Payload),
			X:              80 + (i%6)*190,
			Y:              80 + (i/6)*140,
		})
		if i > 0 {
			edges = append(edges, dagEdge{From: fmt.Sprintf("n%d", i-1), To: nodeID, Kind: "timeline"})
		}
		if ev.Type == core.EventSandboxRestore && snapshotID != "" {
			if from := snapshotNodes[snapshotID]; from != "" && from != nodeID {
				edges = append(edges, dagEdge{From: from, To: nodeID, Kind: "restore"})
			}
		}
	}
	return nodes, edges
}

func dagNodeLabel(ev core.Event, index int) string {
	name := string(ev.Type)
	if len(name) > 24 {
		name = name[:24]
	}
	return fmt.Sprintf("%02d %s", index+1, name)
}

func snapshotIDForEvent(ev core.Event) string {
	switch ev.Type {
	case core.EventSandboxSnapshot:
		var p core.SandboxSnapshotPayload
		if json.Unmarshal(ev.Payload, &p) == nil {
			return strings.TrimSpace(p.SnapshotID)
		}
	case core.EventSandboxRestore:
		var p core.SandboxRestorePayload
		if json.Unmarshal(ev.Payload, &p) == nil {
			return strings.TrimSpace(p.SnapshotID)
		}
	}
	return ""
}

func snapshotTreeSummary(runDir, snapshotID string) string {
	if strings.TrimSpace(snapshotID) == "" {
		return "No sandbox snapshot is associated with this point in the trace."
	}
	root := filepath.Join(runDir, "snapshots", snapshotID)
	entries, err := collectSnapshotEntries(root, 250)
	if err != nil {
		return fmt.Sprintf("Snapshot %s is not available on disk: %v", snapshotID, err)
	}
	if len(entries) == 0 {
		return fmt.Sprintf("Snapshot %s is empty.", snapshotID)
	}
	return fmt.Sprintf("snapshot=%s\n%s", snapshotID, strings.Join(entries, "\n"))
}

func collectSnapshotEntries(root string, limit int) ([]string, error) {
	var entries []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if len(entries) >= limit {
			return filepath.SkipAll
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			entries = append(entries, rel+"/")
			return nil
		}
		info, err := d.Info()
		if err != nil {
			entries = append(entries, rel)
			return nil
		}
		entries = append(entries, fmt.Sprintf("%s  %d bytes", rel, info.Size()))
		return nil
	})
	sort.Strings(entries)
	return entries, err
}

func prettyPayload(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return string(raw)
	}
	return string(out)
}

func traceDAGHTML(runID, dataJSON string) string {
	return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>v100 trace DAG ` + html.EscapeString(runID) + `</title>
<style>
body{margin:0;font:14px/1.4 system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;background:#f7f7f4;color:#1d2428}
.app{display:grid;grid-template-columns:minmax(0,1fr) 420px;height:100vh}
.graph{overflow:auto;padding:22px;background:#fffdf8}
.panel{border-left:1px solid #d8d6cf;background:#f0efea;padding:18px;overflow:auto}
h1{font-size:16px;margin:0 0 14px}
.meta{color:#586168;margin-bottom:14px}
svg{min-width:1240px;min-height:820px}
.edge{stroke:#a8b0b6;stroke-width:2;marker-end:url(#arrow)}
.edge.restore{stroke:#b56a13;stroke-dasharray:6 4}
.node rect{fill:#ffffff;stroke:#9da7ad;stroke-width:1.5;rx:6}
.node text{font-size:12px;fill:#1d2428;pointer-events:none}
.node.snapshot rect{fill:#eaf5ec;stroke:#3b8a58}
.node.restore rect{fill:#fff1df;stroke:#b56a13}
.node.active rect{stroke:#0a66c2;stroke-width:3}
pre{white-space:pre-wrap;background:#fff;border:1px solid #d8d6cf;border-radius:6px;padding:10px;overflow:auto}
.pill{display:inline-block;border:1px solid #c9c6bd;border-radius:999px;padding:2px 8px;margin:2px 4px 8px 0;background:#fff}
button{border:1px solid #b9b6ad;border-radius:6px;background:#fff;padding:6px 10px;cursor:pointer}
</style>
</head>
<body>
<div class="app">
<main class="graph">
<h1>Trace DAG</h1>
<div class="meta">Run ` + html.EscapeString(runID) + ` · click any node to inspect payload and sandbox state.</div>
<svg id="dag" role="img" aria-label="Trace DAG">
<defs><marker id="arrow" markerWidth="8" markerHeight="8" refX="7" refY="3" orient="auto"><path d="M0,0 L0,6 L7,3 z" fill="#a8b0b6"/></marker></defs>
</svg>
</main>
<aside class="panel">
<h1 id="title">Select a node</h1>
<div id="tags"></div>
<h1>Payload</h1>
<pre id="payload">Click a node in the graph.</pre>
<h1>Workspace State</h1>
<pre id="workspace">Click a node in the graph.</pre>
</aside>
</div>
<script>
const data = ` + dataJSON + `;
const svg = document.getElementById('dag');
const nodeById = Object.fromEntries(data.nodes.map(n => [n.id, n]));
function line(x1,y1,x2,y2, cls){
  const el=document.createElementNS('http://www.w3.org/2000/svg','line');
  el.setAttribute('x1',x1); el.setAttribute('y1',y1); el.setAttribute('x2',x2); el.setAttribute('y2',y2); el.setAttribute('class',cls);
  svg.appendChild(el);
}
for (const e of data.edges) {
  const a=nodeById[e.from], b=nodeById[e.to];
  if (a && b) line(a.x+150,a.y+24,b.x,b.y+24,'edge '+e.kind);
}
function showNode(n, group) {
  document.querySelectorAll('.node').forEach(el => el.classList.remove('active'));
  group.classList.add('active');
  document.getElementById('title').textContent = n.label;
  document.getElementById('tags').innerHTML = [
    n.type, n.step_id && 'step='+n.step_id, n.event_id && 'event='+n.event_id, n.snapshot_id && 'snapshot='+n.snapshot_id
  ].filter(Boolean).map(v => '<span class="pill">'+escapeHTML(v)+'</span>').join('');
  document.getElementById('payload').textContent = n.payload || '{}';
  document.getElementById('workspace').textContent = n.workspace_state || 'No workspace state recorded.';
}
function escapeHTML(s){return String(s).replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));}
for (const n of data.nodes) {
  const g=document.createElementNS('http://www.w3.org/2000/svg','g');
  const cls = n.type === 'sandbox.snapshot' ? 'node snapshot' : (n.type === 'sandbox.restore' ? 'node restore' : 'node');
  g.setAttribute('class', cls);
  g.setAttribute('transform', ` + "`translate(${n.x},${n.y})`" + `);
  g.setAttribute('tabindex','0');
  const r=document.createElementNS('http://www.w3.org/2000/svg','rect');
  r.setAttribute('width','150'); r.setAttribute('height','48');
  const t=document.createElementNS('http://www.w3.org/2000/svg','text');
  t.setAttribute('x','10'); t.setAttribute('y','28'); t.textContent=n.label;
  g.appendChild(r); g.appendChild(t);
  g.addEventListener('click',()=>showNode(n,g));
  g.addEventListener('keydown',ev=>{if(ev.key==='Enter')showNode(n,g)});
  svg.appendChild(g);
}
if (data.nodes.length) showNode(data.nodes[0], document.querySelector('.node'));
</script>
</body>
</html>`
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
