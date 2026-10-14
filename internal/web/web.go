// Package web serves the admin panel and REST API.
package web

import (
	"encoding/json"
	"net/http"

	"github.com/project-base-mirror/epic-claim/internal/epic"
)

func New(addr string, ec *epic.Client) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /api/freegames", func(w http.ResponseWriter, r *http.Request) {
		games, err := ec.CurrentFreeGames(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(games)
	})
	// TODO: accounts CRUD, claim history, manual trigger, auth, admin UI.
	return &http.Server{Addr: addr, Handler: mux}
}
