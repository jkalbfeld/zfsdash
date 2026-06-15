package web

import (
	"encoding/json"
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

type Handler struct {
	store *store.Store
	cfg   *config.Config
	tmpl  *template.Template
}

func NewHandler(st *store.Store, cfg *config.Config) *Handler {
	funcMap := template.FuncMap{
		"humanBytes": zfs.BytesToHuman,
		"timeAgo":    timeAgo,
		"pctClass":   pctClass,
	}
	tmpl := template.Must(template.New("").Funcs(funcMap).ParseFS(templateFS, "templates/*.html"))
	return &Handler{store: st, cfg: cfg, tmpl: tmpl}
}

func (h *Handler) Register(r *chi.Mux) {
	r.Get("/", h.dashboard)
	r.Get("/partials/overview", h.partialOverview)
	r.Get("/partials/snapshots", h.partialSnapshots)
	r.Get("/partials/datasets", h.partialDatasets)
	r.Get("/api/data", h.apiData)
	r.Get("/api/health", h.apiHealth)
}

func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	data := h.store.Get()
	if err := h.tmpl.ExecuteTemplate(w, "layout.html", pageData(data, h.cfg)); err != nil {
		log.Printf("template error: %v", err)
	}
}

func (h *Handler) partialOverview(w http.ResponseWriter, r *http.Request) {
	data := h.store.Get()
	if err := h.tmpl.ExecuteTemplate(w, "overview.html", pageData(data, h.cfg)); err != nil {
		log.Printf("template error: %v", err)
	}
}

func (h *Handler) partialSnapshots(w http.ResponseWriter, r *http.Request) {
	data := h.store.Get()
	if err := h.tmpl.ExecuteTemplate(w, "snapshots.html", pageData(data, h.cfg)); err != nil {
		log.Printf("template error: %v", err)
	}
}

func (h *Handler) partialDatasets(w http.ResponseWriter, r *http.Request) {
	data := h.store.Get()
	if err := h.tmpl.ExecuteTemplate(w, "datasets.html", pageData(data, h.cfg)); err != nil {
		log.Printf("template error: %v", err)
	}
}

func (h *Handler) apiData(w http.ResponseWriter, r *http.Request) {
	data := h.store.Get()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func (h *Handler) apiHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}

type PageData struct {
	CollectedAt   time.Time
	Pools         []zfs.Pool
	TopDatasets   []zfs.Dataset
	Snapshots     []zfs.Snapshot
	TotalSnaps    int
	TotalSnapSize uint64
	Error         string
	PollInterval  int
}

func pageData(data *zfs.CollectedData, cfg *config.Config) PageData {
	if data == nil {
		return PageData{Error: "No data collected yet. Check your config and connectivity.", PollInterval: cfg.PollInterval}
	}

	// Top 15 datasets by used bytes
	datasets := make([]zfs.Dataset, len(data.Datasets))
	copy(datasets, data.Datasets)
	sort.Slice(datasets, func(i, j int) bool {
		return datasets[i].UsedBytes > datasets[j].UsedBytes
	})
	if len(datasets) > 15 {
		datasets = datasets[:15]
	}

	// Snapshot totals
	var totalSize uint64
	for _, s := range data.Snapshots {
		totalSize += s.UsedBytes
	}

	return PageData{
		CollectedAt:   data.CollectedAt,
		Pools:         data.Pools,
		TopDatasets:   datasets,
		Snapshots:     data.Snapshots,
		TotalSnaps:    len(data.Snapshots),
		TotalSnapSize: totalSize,
		Error:         data.Error,
		PollInterval:  cfg.PollInterval,
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

func pctClass(pct float64) string {
	switch {
	case pct >= 90:
		return "pct-critical"
	case pct >= 75:
		return "pct-warn"
	default:
		return "pct-ok"
	}
}
