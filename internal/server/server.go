// Package server wires the HTTP surface: ping endpoints, the management API
// and the embedded SPA.
package server

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/yufei/feixi/internal/config"
	"github.com/yufei/feixi/internal/daemon"
	"github.com/yufei/feixi/internal/ping"
	"github.com/yufei/feixi/internal/store"
)

type Server struct {
	Router chi.Router
	DB     *store.DB
	Cfg    *config.Config
}

func New(cfg *config.Config, db *store.DB, d *daemon.Daemon, schemas []string, tester Tester) *Server {
	r := chi.NewRouter()
	r.Use(chimw.Recoverer)
	r.Use(requestLog)

	ph := &ping.Handler{DB: db, BodyLimit: cfg.PingBodyLimit, OnPrune: d.QueuePrune}

	// One dispatcher covers both ping URL shapes: a UUID code, or the
	// project ping_key plus a slug.
	for _, method := range []string{"GET", "POST"} {
		r.Method(method, "/ping/{first}", http.HandlerFunc(ph.Root))
		r.Method(method, "/ping/{first}/{second}", http.HandlerFunc(ph.Root))
		r.Method(method, "/ping/{first}/{second}/{third}", http.HandlerFunc(ph.Root))
	}

	bh := &badgeHandler{db: db, cfg: cfg}
	r.Get("/badge/{badgeKey}/{seg}", bh.badgeAll)
	r.Get("/badge/{badgeKey}/{sig}/{tagfmt}", bh.badgeTag)
	r.Get("/b/{states}/{seg}", bh.checkBadge)

	api := &apiHandler{db: db, cfg: cfg, badges: bh}
	for _, version := range []string{"v1", "v2", "v3"} {
		r.Route("/api/"+version, func(r chi.Router) {
			api.mount(r, version)
			r.Get("/badges/", func(w http.ResponseWriter, req *http.Request) { bh.list(w, req) })
		})
	}

	mountFront(r, db, cfg, schemas, tester)
	mountSPA(r)
	return &Server{Router: r, DB: db, Cfg: cfg}
}

func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if !isChatty(r.URL.Path) {
			logRequest(r, start)
		}
	})
}

// isChatty hides the polling endpoints from the log; they fire every few seconds.
func isChatty(path string) bool {
	return len(path) >= 6 && path[:6] == "/front"
}
