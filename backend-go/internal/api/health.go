package api

import "net/http"

const healthPattern = "GET /api/v1/health"

type healthOut struct {
	Status   string `json:"status"`
	Version  string `json:"version"`
	Database string `json:"database"`
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	database := "ok"
	if err := s.pools.Ping(r.Context()); err != nil {
		database = "unavailable"
	}
	writeJSON(w, http.StatusOK, healthOut{
		Status:   "ok",
		Version:  APIVersion,
		Database: database,
	})
}
