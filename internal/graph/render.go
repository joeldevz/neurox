package graph

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
)

// RenderHTML writes the interactive graph visualization to the given writer.
func RenderHTML(w io.Writer, data *Data) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal graph data: %w", err)
	}

	_, err = fmt.Fprintf(w, htmlTemplate, string(jsonData))
	return err
}

// RenderToFile writes the graph HTML to a file and optionally opens it in the browser.
func RenderToFile(path string, data *Data, openBrowser bool) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	if err := RenderHTML(f, data); err != nil {
		return err
	}

	if openBrowser {
		openURL(path)
	}
	return nil
}

func openURL(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Start()
}

// htmlTemplate is the self-contained HTML visualization.
// The single %s placeholder receives the JSON graph data.
var htmlTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>neurox — graph</title>
<script src="https://unpkg.com/vis-network@9.1.9/standalone/umd/vis-network.min.js"></script>
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  :root {
    --bg: #07080d;
    --surface: rgba(255,255,255,0.03);
    --border: rgba(255,255,255,0.06);
    --text: #c8cad0;
    --text-dim: rgba(255,255,255,0.35);
    --purple: #8b5cf6;
    --blue: #3b82f6;
    --green: #10b981;
    --amber: #f59e0b;
    --red: #ef4444;
    --cyan: #06b6d4;
    --pink: #ec4899;
    --indigo: #6366f1;
    --teal: #14b8a6;
  }
  body {
    font-family: 'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', system-ui, sans-serif;
    background: var(--bg);
    color: var(--text);
    height: 100vh;
    overflow: hidden;
    display: flex;
    flex-direction: column;
  }
  .header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.8rem 1.5rem;
    border-bottom: 1px solid var(--border);
    background: var(--surface);
    flex-shrink: 0;
  }
  .logo {
    font-size: 1rem;
    font-weight: 600;
    letter-spacing: 0.08em;
    background: linear-gradient(135deg, var(--purple), var(--blue));
    -webkit-background-clip: text;
    -webkit-text-fill-color: transparent;
  }
  .stats {
    display: flex;
    gap: 1.5rem;
    font-size: 0.75rem;
    color: var(--text-dim);
  }
  .stats span { color: var(--text); font-weight: 500; }
  .main {
    display: flex;
    flex: 1;
    overflow: hidden;
  }
  .sidebar {
    width: 280px;
    border-right: 1px solid var(--border);
    background: var(--surface);
    padding: 1rem;
    overflow-y: auto;
    flex-shrink: 0;
  }
  .sidebar h3 {
    font-size: 0.7rem;
    text-transform: uppercase;
    letter-spacing: 0.1em;
    color: var(--text-dim);
    margin-bottom: 0.6rem;
  }
  .filter-group { margin-bottom: 1rem; }
  .filter-group select, .filter-group input {
    width: 100%%;
    padding: 0.4rem 0.6rem;
    background: rgba(255,255,255,0.05);
    border: 1px solid var(--border);
    border-radius: 6px;
    color: var(--text);
    font-size: 0.8rem;
    outline: none;
  }
  .filter-group select:focus, .filter-group input:focus {
    border-color: var(--purple);
  }
  .legend { margin-top: 1rem; }
  .legend-item {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-size: 0.75rem;
    margin-bottom: 0.3rem;
    color: var(--text-dim);
  }
  .legend-dot {
    width: 10px;
    height: 10px;
    border-radius: 50%%;
    flex-shrink: 0;
  }
  .legend-line {
    width: 20px;
    height: 2px;
    flex-shrink: 0;
  }
  #graph {
    flex: 1;
    background: var(--bg);
  }
  .detail-panel {
    position: fixed;
    bottom: 1rem;
    right: 1rem;
    width: 380px;
    max-height: 300px;
    background: rgba(7,8,13,0.95);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 1rem;
    overflow-y: auto;
    display: none;
    backdrop-filter: blur(12px);
    z-index: 10;
  }
  .detail-panel.visible { display: block; }
  .detail-panel h4 {
    font-size: 0.85rem;
    margin-bottom: 0.5rem;
    color: var(--text);
  }
  .detail-panel .type-badge {
    display: inline-block;
    padding: 0.15rem 0.5rem;
    border-radius: 4px;
    font-size: 0.65rem;
    font-weight: 600;
    text-transform: uppercase;
    margin-right: 0.3rem;
    margin-bottom: 0.4rem;
  }
  .detail-panel .content {
    font-size: 0.75rem;
    color: var(--text-dim);
    line-height: 1.5;
    white-space: pre-wrap;
    max-height: 150px;
    overflow-y: auto;
  }
  .detail-panel .meta {
    font-size: 0.65rem;
    color: var(--text-dim);
    margin-top: 0.5rem;
    border-top: 1px solid var(--border);
    padding-top: 0.4rem;
  }
</style>
</head>
<body>

<div class="header">
  <div class="logo">neurox graph</div>
  <div class="stats">
    <div>Nodes: <span id="nodeCount">0</span></div>
    <div>Edges: <span id="edgeCount">0</span></div>
    <div>Total obs: <span id="totalObs">0</span></div>
    <div>Total links: <span id="totalLinks">0</span></div>
  </div>
</div>

<div class="main">
  <div class="sidebar">
    <div class="filter-group">
      <h3>Filter by type</h3>
      <select id="filterType">
        <option value="">All types</option>
        <option value="decision">decision</option>
        <option value="bugfix">bugfix</option>
        <option value="discovery">discovery</option>
        <option value="pattern">pattern</option>
        <option value="gotcha">gotcha</option>
        <option value="config">config</option>
        <option value="preference">preference</option>
        <option value="question">question</option>
      </select>
    </div>
    <div class="filter-group">
      <h3>Filter by layer</h3>
      <select id="filterLayer">
        <option value="">All layers</option>
        <option value="0">Buffer (0)</option>
        <option value="1">Working (1)</option>
        <option value="2">Core (2)</option>
      </select>
    </div>
    <div class="filter-group">
      <h3>Filter by kind</h3>
      <select id="filterKind">
        <option value="">All kinds</option>
        <option value="episodic">episodic</option>
        <option value="semantic">semantic</option>
        <option value="procedural">procedural</option>
      </select>
    </div>
    <div class="filter-group">
      <h3>Min importance</h3>
      <input type="range" id="filterImportance" min="0" max="1" step="0.05" value="0">
      <div style="font-size:0.7rem;color:var(--text-dim);margin-top:0.2rem">
        >= <span id="importanceVal">0.00</span>
      </div>
    </div>
    <div class="filter-group">
      <h3>Search</h3>
      <input type="text" id="filterSearch" placeholder="Search titles...">
    </div>

    <div class="legend">
      <h3>Node colors (type)</h3>
      <div class="legend-item"><div class="legend-dot" style="background:#8b5cf6"></div> decision</div>
      <div class="legend-item"><div class="legend-dot" style="background:#ef4444"></div> bugfix</div>
      <div class="legend-item"><div class="legend-dot" style="background:#3b82f6"></div> discovery</div>
      <div class="legend-item"><div class="legend-dot" style="background:#10b981"></div> pattern</div>
      <div class="legend-item"><div class="legend-dot" style="background:#f59e0b"></div> gotcha</div>
      <div class="legend-item"><div class="legend-dot" style="background:#06b6d4"></div> config</div>
      <div class="legend-item"><div class="legend-dot" style="background:#ec4899"></div> preference</div>
      <div class="legend-item"><div class="legend-dot" style="background:#6366f1"></div> question</div>
    </div>

    <div class="legend" style="margin-top:0.8rem">
      <h3>Edge styles (relation)</h3>
      <div class="legend-item"><div class="legend-line" style="background:#ef4444"></div> supersedes</div>
      <div class="legend-item"><div class="legend-line" style="background:#f59e0b"></div> contradicts</div>
      <div class="legend-item"><div class="legend-line" style="background:rgba(255,255,255,0.2)"></div> relates_to</div>
      <div class="legend-item"><div class="legend-line" style="background:#3b82f6"></div> derived_from</div>
      <div class="legend-item"><div class="legend-line" style="background:#10b981"></div> validates</div>
      <div class="legend-item"><div class="legend-line" style="background:#8b5cf6"></div> refines</div>
    </div>
  </div>

  <div id="graph"></div>
</div>

<div class="detail-panel" id="detailPanel">
  <h4 id="detailTitle"></h4>
  <div id="detailBadges"></div>
  <div class="content" id="detailContent"></div>
  <div class="meta" id="detailMeta"></div>
</div>

<script>
const GRAPH_DATA = %s;

const TYPE_COLORS = {
  decision:   '#8b5cf6',
  bugfix:     '#ef4444',
  discovery:  '#3b82f6',
  pattern:    '#10b981',
  gotcha:     '#f59e0b',
  config:     '#06b6d4',
  preference: '#ec4899',
  question:   '#6366f1'
};

const EDGE_COLORS = {
  supersedes:   '#ef4444',
  contradicts:  '#f59e0b',
  relates_to:   'rgba(255,255,255,0.15)',
  derived_from: '#3b82f6',
  validates:    '#10b981',
  refines:      '#8b5cf6'
};

const EDGE_DASHES = {
  supersedes:   false,
  contradicts:  [5, 5],
  relates_to:   [2, 4],
  derived_from: false,
  validates:    [8, 4],
  refines:      false
};

// Update stats display.
document.getElementById('nodeCount').textContent = GRAPH_DATA.stats.shown_nodes;
document.getElementById('edgeCount').textContent = GRAPH_DATA.stats.shown_edges;
document.getElementById('totalObs').textContent = GRAPH_DATA.stats.total_observations;
document.getElementById('totalLinks').textContent = GRAPH_DATA.stats.total_links;

// Build node lookup.
const nodeLookup = {};
GRAPH_DATA.nodes.forEach(n => { nodeLookup[n.id] = n; });

// Create vis datasets.
const visNodes = new vis.DataSet();
const visEdges = new vis.DataSet();
let allVisNodes = [];
let allVisEdges = [];

function buildVisNode(n) {
  const color = TYPE_COLORS[n.observation_type] || '#666';
  const size = 8 + (n.importance || 0) * 25;
  const borderWidth = n.layer === 2 ? 3 : n.layer === 1 ? 2 : 1;
  return {
    id: n.id,
    label: n.title.length > 40 ? n.title.substring(0, 37) + '...' : n.title,
    title: n.title,
    size: size,
    color: {
      background: color,
      border: color,
      highlight: { background: color, border: '#fff' },
      hover: { background: color, border: '#fff' }
    },
    borderWidth: borderWidth,
    font: { color: '#c8cad0', size: 10, face: 'Inter, sans-serif' },
    _data: n
  };
}

function buildVisEdge(e) {
  const color = EDGE_COLORS[e.relation_type] || 'rgba(255,255,255,0.1)';
  const dashes = EDGE_DASHES[e.relation_type] || false;
  return {
    id: e.id,
    from: e.source,
    to: e.target,
    color: { color: color, highlight: '#fff', hover: color, opacity: 0.6 },
    dashes: dashes,
    width: 1 + (e.confidence || 0),
    arrows: { to: { enabled: true, scaleFactor: 0.5 } },
    title: e.relation_type,
    _data: e
  };
}

// Initial build.
GRAPH_DATA.nodes.forEach(n => allVisNodes.push(buildVisNode(n)));
GRAPH_DATA.edges.forEach(e => allVisEdges.push(buildVisEdge(e)));
visNodes.add(allVisNodes);
visEdges.add(allVisEdges);

// Create network.
const container = document.getElementById('graph');
const network = new vis.Network(container, { nodes: visNodes, edges: visEdges }, {
  physics: {
    stabilization: { iterations: 150, updateInterval: 25 },
    barnesHut: {
      gravitationalConstant: -3000,
      centralGravity: 0.3,
      springLength: 120,
      springConstant: 0.02,
      damping: 0.4
    }
  },
  interaction: {
    hover: true,
    tooltipDelay: 200,
    zoomView: true,
    dragView: true
  },
  nodes: {
    shape: 'dot',
    scaling: { min: 8, max: 35 }
  },
  edges: {
    smooth: { type: 'continuous' }
  }
});

// Detail panel on click.
const panel = document.getElementById('detailPanel');
network.on('click', function(params) {
  if (params.nodes.length > 0) {
    const nodeId = params.nodes[0];
    const n = nodeLookup[nodeId];
    if (!n) return;

    document.getElementById('detailTitle').textContent = n.title;

    const badgesHtml = [
      '<span class="type-badge" style="background:' + (TYPE_COLORS[n.observation_type] || '#666') + '22;color:' + (TYPE_COLORS[n.observation_type] || '#666') + '">' + n.observation_type + '</span>',
      '<span class="type-badge" style="background:rgba(255,255,255,0.05);color:var(--text-dim)">' + n.kind + '</span>',
      '<span class="type-badge" style="background:rgba(255,255,255,0.05);color:var(--text-dim)">L' + n.layer + '</span>'
    ];
    if (n.tags && n.tags.length) {
      n.tags.forEach(t => {
        badgesHtml.push('<span class="type-badge" style="background:rgba(255,255,255,0.05);color:var(--text-dim)">' + t + '</span>');
      });
    }
    document.getElementById('detailBadges').innerHTML = badgesHtml.join('');
    document.getElementById('detailContent').textContent = n.content || '(no content loaded)';
    document.getElementById('detailMeta').innerHTML =
      'Importance: ' + (n.importance || 0).toFixed(3) +
      ' &middot; Confidence: ' + (n.confidence || 0).toFixed(2) +
      ' &middot; Staleness: ' + (n.staleness || 'fresh') +
      ' &middot; ' + (n.created_at || '');

    panel.classList.add('visible');
  } else {
    panel.classList.remove('visible');
  }
});

// Filtering.
function applyFilters() {
  const typeFilter = document.getElementById('filterType').value;
  const layerFilter = document.getElementById('filterLayer').value;
  const kindFilter = document.getElementById('filterKind').value;
  const impFilter = parseFloat(document.getElementById('filterImportance').value);
  const searchFilter = document.getElementById('filterSearch').value.toLowerCase();

  const visibleIDs = new Set();

  const filteredNodes = allVisNodes.filter(vn => {
    const n = vn._data;
    if (typeFilter && n.observation_type !== typeFilter) return false;
    if (layerFilter !== '' && n.layer !== parseInt(layerFilter)) return false;
    if (kindFilter && n.kind !== kindFilter) return false;
    if (n.importance < impFilter) return false;
    if (searchFilter && !n.title.toLowerCase().includes(searchFilter)) return false;
    return true;
  });

  filteredNodes.forEach(vn => visibleIDs.add(vn.id));

  const filteredEdges = allVisEdges.filter(ve => {
    return visibleIDs.has(ve.from) && visibleIDs.has(ve.to);
  });

  visNodes.clear();
  visEdges.clear();
  visNodes.add(filteredNodes);
  visEdges.add(filteredEdges);

  document.getElementById('nodeCount').textContent = filteredNodes.length;
  document.getElementById('edgeCount').textContent = filteredEdges.length;
}

document.getElementById('filterType').addEventListener('change', applyFilters);
document.getElementById('filterLayer').addEventListener('change', applyFilters);
document.getElementById('filterKind').addEventListener('change', applyFilters);
document.getElementById('filterImportance').addEventListener('input', function() {
  document.getElementById('importanceVal').textContent = parseFloat(this.value).toFixed(2);
  applyFilters();
});
document.getElementById('filterSearch').addEventListener('input', applyFilters);

</script>
</body>
</html>`
