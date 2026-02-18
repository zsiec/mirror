package server

import (
	"encoding/json"
	"net/http"

	"github.com/zsiec/mirror/pkg/version"
)

// handleVersion handles the /version endpoint
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	versionInfo := version.GetInfo()

	data, err := json.Marshal(versionInfo)
	if err != nil {
		s.logger.WithError(err).Error("Failed to encode version response")
		s.errorHandler.HandleError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(data)
}

// handleStreamsPlaceholder is a placeholder for the streams endpoint when ingestion is not enabled
func (s *Server) handleStreamsPlaceholder(w http.ResponseWriter, r *http.Request) {
	response := struct {
		Message string `json:"message"`
		Phase   string `json:"phase"`
	}{
		Message: "Streams endpoint requires ingestion to be enabled",
		Phase:   "2",
	}

	data, err := json.Marshal(response)
	if err != nil {
		s.logger.WithError(err).Error("Failed to encode response")
		s.errorHandler.HandleError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

// writeJSON is a helper to write JSON responses
func (s *Server) writeJSON(w http.ResponseWriter, status int, data interface{}) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(data)
}

// writeError is a helper to write error responses
func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	s.errorHandler.HandleError(w, r, err)
}
