package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jkalbfeld/zfsdash/internal/config"
	"github.com/jkalbfeld/zfsdash/internal/store"
	"github.com/jkalbfeld/zfsdash/internal/zfs"
)

// Handler holds the web layer.
type Handler struct {
	st   *store.Store
	cfg  *config.Config
	tmpl *template.Template
}

func NewHandler(st *store.Store, cfg *config.Config) *Handler {
	funcMap := template.FuncMap{
		"formatBytes": formatBytes,
		"formatPercent": func(f float64) string { return fmt.Sprintf("%.1f%%", f) },
		"healthClass": healthClass,
		"now": func() string { return time.Now().Format("2006-01-02 15:04:05") },
		"timeAgo": timeAgo,
		"sub": func(a, b int) int { return a - b },
		"add": func(a, b int) int { return a + b },
		"mul": func(a, b float64) float64 { return a * b },
		"div": func(a, b float64) float64 {
			if b == 0 { return 0 }
			return a / b
		},
		"gt": func(a, b float64) bool { return a > b },
		"deref": func(d *zfs.CollectedData) zfs.CollectedData {
			if d == nil { return zfs.CollectedData{} }
			return *d
		},
	}
	tmpl := template.Must(template.New("").Funcs(funcMap).ParseFS(templateFS, "templates/*.html"))
	return &Handler{st: st, cfg: cfg, tmpl: tmpl}
}

// Register attaches all routes to the router.
func (h *Handler) Register(r chi.Router) {
	r.Get("/", h.dashboard)
	r.Get("/pools", h.pools)
	r.Get("/datasets", h.datasets)
	r.Get("/snapshots", h.snapshots)
	r.Get("/alerts", h.alertsPage)

	// HTMX partial endpoints
	r.Get("/htmx/pools", h.htmxPools)
	r.Get("/htmx/datasets", h.htmxDatasets)
	r.Get("/htmx/snapshots", h.htmxSnapshots)
	r.Get("/htmx/header", h.htmxHeader)

	// API endpoints
	r.Get("/api/v1/pools", h.apiPools)
	r.Get("/api/v1/datasets", h.apiDatasets)
	r.Get("/api/v1/snapshots", h.apiSnapshots)
	r.Get("/api/v1/health", h.apiHealth)
}

// --- page handlers ---

func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	h.render(w, "dashboard.html", h.buildViewData())
}

func (h *Handler) pools(w http.ResponseWriter, r *http.Request) {
	h.render(w, "pools.html", h.buildViewData())
}

func (h *Handler) datasets(w http.ResponseWriter, r *http.Request) {
	h.render(w, "datasets.html", h.buildViewData())
}

func (h *Handler) snapshots(w http.ResponseWriter, r *http.Request) {
	h.render(w, "snapshots.html", h.buildViewData())
}

func (h *Handler) alertsPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, "alertspage.html", h.buildViewData())
}

// --- HTMX partial handlers ---

func (h *Handler) htmxPools(w http.ResponseWriter, r *http.Request) {
	h.renderPartial(w, "partial_pools.html", h.buildViewData())
}

func (h *Handler) htmxDatasets(w http.ResponseWriter, r *http.Request) {
	h.renderPartial(w, "partial_datasets.html", h.buildViewData())
}

func (h *Handler) htmxSnapshots(w http.ResponseWriter, r *http.Request) {
	h.renderPartial(w, "partial_snapshots.html", h.buildViewData())
}

func (h *Handler) htmxHeader(w http.ResponseWriter, r *http.Request) {
	h.renderPartial(w, "partial_header.html", h.buildViewData())
}

// --- API handlers ---

func (h *Handler) apiPools(w http.ResponseWriter, r *http.Request) {
	d := h.st.Get()
	if d == nil {
		http.Error(w, "no data", http.StatusServiceUnavailable)
		return
	}
	jsonResponse(w, d.Pools)
}

func (h *Handler) apiDatasets(w http.ResponseWriter, r *http.Request) {
	d := h.st.Get()
	if d == nil {
		http.Error(w, "no data", http.StatusServiceUnavailable)
		return
	}
	jsonResponse(w, d.Datasets)
}

func (h *Handler) apiSnapshots(w http.ResponseWriter, r *http.Request) {
	d := h.st.Get()
	if d == nil {
		http.Error(w, "no data", http.StatusServiceUnavailable)
		return
	}
	jsonResponse(w, d.Snapshots)
}

func (h *Handler) apiHealth(w http.ResponseWriter, r *http.Request) {
	d := h.st.Get()
	if d == nil {
		http.Error(w, `{"status":"no_data"}`, http.StatusServiceUnavailable)
		return
	}
	overall := "ONLINE"
	for _, p := range d.Pools {
		if p.Health != "ONLINE" {
			overall = p.Health
			break
		}
	}
	jsonResponse(w, map[string]interface{}{
		"status":       overall,
		"pools":        len(d.Pools),
		"collected_at": d.CollectedAt,
	})
}

// --- view data ---

type ViewData struct {
	CollectedAt  time.Time
	Pools        []zfs.Pool
	Datasets     []zfs.Dataset
	Snapshots    []zfs.Snapshot
	TopDatasets  []zfs.Dataset // top 10 by used bytes
	PoolCount    int
	Healthy      int
	Degraded     int
	SnapshotCount int
	TotalUsed    uint64
	TotalSize    uint64
	Config       *config.Config
}

func (h *Handler) buildViewData() ViewData {
	d := h.st.Get()
	if d == nil {
		return ViewData{Config: h.cfg}
	}
	vd := ViewData{
		CollectedAt:   d.CollectedAt,
		Pools:         d.Pools,
		Datasets:      d.Datasets,
		Snapshots:     d.Snapshots,
		SnapshotCount: len(d.Snapshots),
		PoolCount:     len(d.Pools),
		Config:        h.cfg,
	}
	for _, p := range d.Pools {
		if p.Health == "ONLINE" {
			vd.Healthy++
		} else {
			vd.Degraded++
		}
		vd.TotalSize += p.Size
		vd.TotalUsed += p.Alloc
	}
	// Top datasets by used
	sorted := make([]zfs.Dataset, len(d.Datasets))
	copy(sorted, d.Datasets)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Used > sorted[j].Used })
	if len(sorted) > 10 {
		sorted = sorted[:10]
	}
	vd.TopDatasets = sorted
	return vd
}

// --- render helpers ---

func (h *Handler) render(w http.ResponseWriter, name string, data interface{}) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("template %s: %v", name, err)
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func (h *Handler) renderPartial(w http.ResponseWriter, name string, data interface{}) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("partial %s: %v", name, err)
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func jsonResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("json encode: %v", err)
	}
}

// --- template helper functions ---

func formatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

func healthClass(health string) string {
	switch health {
	case "ONLINE":
		return "badge-online"
	case "DEGRADED":
		return "badge-degraded"
	case "FAULTED", "UNAVAIL":
		return "badge-faulted"
	default:
		return "badge-offline"
	}
}

func timeAgo(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
