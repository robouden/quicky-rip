// Package admin is a tabbed admin UI for the knobs in internal/settings, the
// recent-emails table, and the in-memory log tail. No framework: one
// handler, one template, basic auth if a password is set.
package admin

import (
	"context"
	"html/template"
	"log/slog"
	"net/http"

	"codeberg.org/Safecast/quicky/internal/logbuf"
	"codeberg.org/Safecast/quicky/internal/settings"
	"codeberg.org/Safecast/quicky/internal/store"
)

type Handler struct {
	Settings *settings.Store
	Logs     *logbuf.Buffer
	DB       *store.DB
	Password string // basic auth password; empty disables auth (dev only)
	Log      *slog.Logger
}

var page = template.Must(template.New("admin").Parse(`<!doctype html>
<html><head><title>quicky admin</title>
<style>
:root{
  --bg:#1a1d22; --bg-alt:#20242b; --bg-input:#262b33;
  --border:#333941; --text:#e7e9ec; --text-dim:#8b929c;
  --accent:#e2933f; --accent-dim:#3a2c1c;
  --pill-newsletter-bg:#3a2c1c; --pill-newsletter:#e2933f;
  --pill-transactional-bg:#1c2f3a; --pill-transactional:#4fa8e2;
  --pill-ambiguous-bg:#2c2f33; --pill-ambiguous:#9aa1ab;
}
*{box-sizing:border-box}
body{font-family:system-ui,sans-serif;margin:0;background:var(--bg);color:var(--text)}
.topbar{background:var(--bg-alt);border-bottom:1px solid var(--border);
  padding:1.1rem 1.5rem;border-top:3px solid var(--accent)}
.topbar h1{margin:0;font-size:1.3rem;letter-spacing:.04em;font-weight:800}
.topbar p{margin:.2rem 0 0;color:var(--text-dim);font-size:.85rem}

.tabs{display:flex;gap:1.75rem;background:var(--bg-alt);border-bottom:1px solid var(--border);
  padding:0 1.5rem}
.tabs button{margin:0;padding:.8rem 0;border:none;background:none;font:inherit;font-size:.92rem;
  font-weight:600;color:var(--text-dim);border-bottom:2px solid transparent;cursor:pointer}
.tabs button.active{color:var(--accent);border-bottom-color:var(--accent)}
.tabs button:hover{color:var(--text)}

main{width:100%;margin:0 auto;padding:1.5rem}
.panel{display:none}
.panel.active{display:block}
h2{margin:0 0 .25rem;font-size:1.1rem}
.hint{color:var(--text-dim);font-size:.82rem}
.msg{color:#5cc27a;margin-top:.75rem}
.err{color:#e26a6a}

label{display:block;margin-top:1rem;font-weight:600;font-size:.9rem}
select,input[type=text],input:not([type]){
  width:100%;padding:.5rem .6rem;margin-top:.3rem;background:var(--bg-input);
  border:1px solid var(--border);border-radius:5px;color:var(--text);font:inherit}
select:focus,input:focus{outline:none;border-color:var(--accent)}
button.primary{margin-top:1.5rem;padding:.55rem 1.4rem;cursor:pointer;background:var(--accent);
  color:#1a1300;border:none;border-radius:5px;font-weight:700}
button.primary:hover{filter:brightness(1.08)}

.table-wrap{overflow-x:auto;margin-top:1rem;border:1px solid var(--border);border-radius:6px}
table{border-collapse:collapse;width:100%;table-layout:fixed}
th,td{text-align:left;padding:.5rem .6rem;vertical-align:top;overflow:hidden;
  text-overflow:ellipsis;position:relative}
td{white-space:normal;word-break:break-word;border-bottom:1px solid var(--border);font-size:.85rem}
tbody tr:hover td{background:#22262d}
thead tr.col-labels th{background:var(--bg-alt);color:#d8b25c;font-size:.78rem;
  text-transform:uppercase;letter-spacing:.04em;cursor:pointer;user-select:none;white-space:nowrap;
  border-bottom:1px solid var(--border)}
thead tr.col-filters th{background:var(--bg-alt);padding:.4rem .5rem;border-bottom:1px solid var(--border)}
thead tr.col-filters input,thead tr.col-filters select{margin:0;font-size:.8rem;padding:.35rem .5rem}
th .arrow{opacity:.4;margin-left:.25rem;font-size:.7em}
th.sorted .arrow{opacity:1}
.resizer{position:absolute;right:0;top:0;width:6px;height:100%;cursor:col-resize;z-index:1}
tr.hidden-row{display:none}

.pill{display:inline-block;padding:.15rem .55rem;border-radius:99px;font-size:.78rem;font-weight:600}
.pill-newsletter{background:var(--pill-newsletter-bg);color:var(--pill-newsletter)}
.pill-transactional{background:var(--pill-transactional-bg);color:var(--pill-transactional)}
.pill-ambiguous{background:var(--pill-ambiguous-bg);color:var(--pill-ambiguous)}

#logs{background:#0e1013;color:#ccd;padding:1rem;height:420px;overflow-y:auto;
  font:.8rem/1.4 ui-monospace,monospace;white-space:pre-wrap;word-break:break-all;
  border:1px solid var(--border);border-radius:6px;margin-top:1rem}
</style></head>
<body>

<div class="topbar">
  <h1>QUICKY ADMIN</h1>
  <p>Email digest pipeline — settings, activity &amp; logs</p>
</div>

<div class="tabs">
  <button data-tab="settings" class="active">Settings</button>
  <button data-tab="emails">Emails</button>
  <button data-tab="logs">Logs</button>
</div>

<main>

<div id="tab-settings" class="panel active">
  <h2>Settings</h2>
  {{if .Saved}}<p class="msg">Saved.</p>{{end}}
  <form method="post" action="/admin">
    <label>LLM provider
      <select name="llm_provider">
        <option value="anthropic" {{if eq .Values.llm_provider "anthropic"}}selected{{end}}>Anthropic (cloud)</option>
        <option value="ollama" {{if eq .Values.llm_provider "ollama"}}selected{{end}}>Ollama (local)</option>
      </select>
    </label>

    <label>Anthropic fast model
      <input name="model_fast" value="{{.Values.model_fast}}">
    </label>
    <label>Anthropic strong model
      <input name="model_strong" value="{{.Values.model_strong}}">
    </label>

    <label>Ollama base URL
      <input name="ollama_url" value="{{.Values.ollama_url}}">
    </label>
    <label>Ollama model
      <input name="ollama_model" value="{{.Values.ollama_model}}">
      <span class="hint">Used for every stage when provider is "ollama".</span>
    </label>

    <button class="primary" type="submit">Save</button>
  </form>
</div>

<div id="tab-emails" class="panel">
  <h2>Recent emails <span class="hint">(last {{len .Emails}})</span></h2>
  <p class="hint">Click a column header to sort, drag its right edge to resize, or type in the row below the headers to filter.</p>
  <div class="table-wrap">
  <table id="emailsTable">
    <colgroup>
      <col style="width:12%"><col style="width:15%"><col style="width:17%">
      <col style="width:13%"><col style="width:25%"><col style="width:6%"><col style="width:12%">
    </colgroup>
    <thead>
      <tr class="col-labels">
        <th data-col="0">Received<span class="arrow"></span></th>
        <th data-col="1">From<span class="arrow"></span></th>
        <th data-col="2">Subject<span class="arrow"></span></th>
        <th data-col="3">Classification<span class="arrow"></span></th>
        <th data-col="4">Summary<span class="arrow"></span></th>
        <th data-col="5">Attempts<span class="arrow"></span></th>
        <th data-col="6">Error<span class="arrow"></span></th>
      </tr>
      <tr class="col-filters">
        <th><input data-col="0" type="text" placeholder="Date…"></th>
        <th><input data-col="1" type="text" placeholder="From…"></th>
        <th><input data-col="2" type="text" placeholder="Subject…"></th>
        <th>
          <select data-col="3">
            <option value="">All classifications</option>
            <option value="newsletter">Newsletter</option>
            <option value="transactional">Transactional</option>
            <option value="ambiguous">Ambiguous</option>
          </select>
        </th>
        <th><input data-col="4" type="text" placeholder="Summary…"></th>
        <th><input data-col="5" type="text" placeholder="Attempts…"></th>
        <th><input data-col="6" type="text" placeholder="Error…"></th>
      </tr>
    </thead>
    <tbody>
    {{range .Emails}}
    <tr>
      <td data-sort="{{.CreatedAt.Unix}}">{{.CreatedAt.Format "2006-01-02 15:04:05"}}</td>
      <td>{{.FromEmail}}</td>
      <td>{{.Subject}}</td>
      <td><span class="pill pill-{{.Classification}}">{{.Classification}}</span>{{if .ClassifierNote}} <span class="hint">({{.ClassifierNote}})</span>{{end}}</td>
      <td>{{if .ParsedSummary}}{{.ParsedSummary}}{{else}}<span class="hint">—</span>{{end}}</td>
      <td>{{.Attempts}}</td>
      <td class="err">{{if .LastError}}{{.LastError}}{{end}}</td>
    </tr>
    {{end}}
    </tbody>
  </table>
  </div>
</div>

<div id="tab-logs" class="panel">
  <h2>Log tail <span class="hint">(last {{.LogMax}} lines, refreshes every 3s)</span></h2>
  <pre id="logs">{{range .LogLines}}{{.}}
{{end}}</pre>
</div>

</main>

<script>
// ---- tabs, restored from #hash ----
const tabs = document.querySelectorAll('.tabs button');
function showTab(name) {
  tabs.forEach(b => b.classList.toggle('active', b.dataset.tab === name));
  document.querySelectorAll('.panel').forEach(p => p.classList.toggle('active', p.id === 'tab-' + name));
}
tabs.forEach(b => b.addEventListener('click', () => {
  location.hash = b.dataset.tab;
  showTab(b.dataset.tab);
}));
showTab((location.hash || '#settings').slice(1));

// ---- emails table: sort, resize, per-column filter ----
const table = document.getElementById('emailsTable');
if (table) {
  const labelRow = table.querySelector('tr.col-labels');
  const headers = labelRow.querySelectorAll('th');
  const cols = table.querySelectorAll('colgroup col');
  let sortCol = -1, sortAsc = true;

  headers.forEach((th, i) => {
    const resizer = document.createElement('span');
    resizer.className = 'resizer';
    th.appendChild(resizer);

    th.addEventListener('click', (e) => {
      if (e.target === resizer) return;
      sortAsc = (sortCol === i) ? !sortAsc : true;
      sortCol = i;
      headers.forEach(h => h.classList.remove('sorted'));
      th.classList.add('sorted');
      headers.forEach((h, hi) => h.querySelectorAll('.arrow').forEach(a => a.textContent = hi === i ? (sortAsc ? '▲' : '▼') : ''));

      const tbody = table.querySelector('tbody');
      const rows = Array.from(tbody.querySelectorAll('tr'));
      rows.sort((r1, r2) => {
        const c1 = r1.children[i], c2 = r2.children[i];
        const v1 = c1.dataset.sort ?? c1.textContent.trim().toLowerCase();
        const v2 = c2.dataset.sort ?? c2.textContent.trim().toLowerCase();
        const n1 = parseFloat(v1), n2 = parseFloat(v2);
        let cmp;
        if (!isNaN(n1) && !isNaN(n2) && String(n1) === v1 && String(n2) === v2) {
          cmp = n1 - n2;
        } else {
          cmp = v1 < v2 ? -1 : v1 > v2 ? 1 : 0;
        }
        return sortAsc ? cmp : -cmp;
      });
      rows.forEach(r => tbody.appendChild(r));
    });

    let startX, startWidth;
    resizer.addEventListener('mousedown', (e) => {
      startX = e.pageX;
      startWidth = cols[i].offsetWidth || th.offsetWidth;
      const onMove = (e2) => { cols[i].style.width = Math.max(40, startWidth + e2.pageX - startX) + 'px'; };
      const onUp = () => { document.removeEventListener('mousemove', onMove); document.removeEventListener('mouseup', onUp); };
      document.addEventListener('mousemove', onMove);
      document.addEventListener('mouseup', onUp);
      e.preventDefault();
      e.stopPropagation();
    });
  });

  const filterControls = table.querySelectorAll('tr.col-filters [data-col]');
  function applyFilters() {
    const active = Array.from(filterControls)
      .map(c => ({ col: +c.dataset.col, val: c.value.trim().toLowerCase() }))
      .filter(f => f.val !== '');
    table.querySelectorAll('tbody tr').forEach(row => {
      const visible = active.every(f => row.children[f.col].textContent.toLowerCase().includes(f.val));
      row.classList.toggle('hidden-row', !visible);
    });
  }
  filterControls.forEach(c => c.addEventListener('input', applyFilters));
}

// ---- log tail polling ----
const logs = document.getElementById('logs');
logs.scrollTop = logs.scrollHeight;
async function poll() {
  try {
    const res = await fetch('/admin/logs');
    if (res.ok) {
      const atBottom = logs.scrollTop + logs.clientHeight >= logs.scrollHeight - 10;
      logs.textContent = await res.text();
      if (atBottom) logs.scrollTop = logs.scrollHeight;
    }
  } catch (e) {}
  setTimeout(poll, 3000);
}
setTimeout(poll, 3000);
</script>
</body></html>`))

type pageData struct {
	Values   map[string]string
	Saved    bool
	LogLines []string
	LogMax   int
	Emails   []store.EmailSummary
}

func (h *Handler) authorized(w http.ResponseWriter, r *http.Request) bool {
	if h.Password == "" {
		return true
	}
	user, pass, ok := r.BasicAuth()
	if !ok || user != "admin" || pass != h.Password {
		w.Header().Set("WWW-Authenticate", `Basic realm="quicky admin"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

// LogsHandler serves the raw log tail as plain text, polled by the admin
// page's JS.
func (h *Handler) LogsHandler(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(w, r) {
		return
	}
	w.Header().Set("content-type", "text/plain; charset=utf-8")
	for _, line := range h.Logs.Lines() {
		w.Write([]byte(line))
		w.Write([]byte("\n"))
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(w, r) {
		return
	}

	ctx := r.Context()
	saved := false

	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		for _, key := range []string{
			settings.LLMProvider, settings.ModelFast, settings.ModelStrong,
			settings.OllamaURL, settings.OllamaModel,
		} {
			if err := h.setIfPresent(ctx, r, key); err != nil {
				h.Log.Error("admin: save setting", "key", key, "err", err)
				http.Error(w, "save failed", http.StatusInternalServerError)
				return
			}
		}
		saved = true
	}

	values, err := h.Settings.All(ctx)
	if err != nil {
		h.Log.Error("admin: load settings", "err", err)
		http.Error(w, "load failed", http.StatusInternalServerError)
		return
	}

	emails, err := h.DB.RecentEmails(ctx, 200)
	if err != nil {
		h.Log.Error("admin: load emails", "err", err)
		http.Error(w, "load failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("content-type", "text/html; charset=utf-8")
	err = page.Execute(w, pageData{
		Values:   values,
		Saved:    saved,
		LogLines: h.Logs.Lines(),
		LogMax:   500,
		Emails:   emails,
	})
	if err != nil {
		h.Log.Error("admin: render", "err", err)
	}
}

func (h *Handler) setIfPresent(ctx context.Context, r *http.Request, key string) error {
	if !r.PostForm.Has(key) {
		return nil
	}
	return h.Settings.Set(ctx, key, r.PostFormValue(key))
}
