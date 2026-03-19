package api

import (
	"embed"
	"net/http"
)

//go:embed dashboard.html
var dashboardFS embed.FS

func (s *Server) handleDashboard(w http.ResponseWriter, _ *http.Request) {
	data, err := dashboardFS.ReadFile("dashboard.html")
	if err != nil {
		http.Error(w, "dashboard not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}
