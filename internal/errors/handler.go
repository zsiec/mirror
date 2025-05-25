package errors

import (
	"encoding/json"
	"net/http"

	"github.com/sirupsen/logrus"
)

// ErrorResponse represents the error response structure.
type ErrorResponse struct {
	Error    ErrorDetails `json:"error"`
	TraceID  string       `json:"trace_id,omitempty"`
	Metadata interface{}  `json:"metadata,omitempty"`
}

// ErrorDetails contains the error details.
type ErrorDetails struct {
	Type    ErrorType              `json:"type"`
	Message string                 `json:"message"`
	Code    string                 `json:"code,omitempty"`
	Details map[string]interface{} `json:"details,omitempty"`
}

// ErrorHandler handles error responses.
type ErrorHandler struct {
	logger *logrus.Logger
}

// NewErrorHandler creates a new error handler.
func NewErrorHandler(logger *logrus.Logger) *ErrorHandler {
	return &ErrorHandler{
		logger: logger,
	}
}

// HandleError handles an error and writes the appropriate response.
func (h *ErrorHandler) HandleError(w http.ResponseWriter, r *http.Request, err error) {
	// Extract trace ID from request context if available
	traceID := r.Header.Get("X-Request-ID")

	// Convert to AppError if it's not already
	var appErr *AppError
	var ok bool

	if appErr, ok = GetAppError(err); !ok {
		// Convert standard errors to AppError
		appErr = WrapInternalError(err, "An unexpected error occurred")
	}

	// Log the error
	logEntry := h.logger.WithFields(logrus.Fields{
		"error_type": appErr.Type,
		"error_code": appErr.Code,
		"trace_id":   traceID,
		"method":     r.Method,
		"path":       r.URL.Path,
		"remote_ip":  r.RemoteAddr,
	})

	// Log at appropriate level
	switch appErr.HTTPStatus {
	case http.StatusInternalServerError, http.StatusServiceUnavailable:
		logEntry.Error(appErr.Error())
	case http.StatusBadRequest, http.StatusNotFound, http.StatusConflict:
		logEntry.Warn(appErr.Error())
	default:
		logEntry.Info(appErr.Error())
	}

	// Create error response
	response := ErrorResponse{
		Error: ErrorDetails{
			Type:    appErr.Type,
			Message: appErr.Message,
			Code:    appErr.Code,
			Details: appErr.Details,
		},
		TraceID: traceID,
	}

	// Write response
	h.writeJSON(w, appErr.HTTPStatus, response)
}

// HandleNotFound handles 404 errors.
func (h *ErrorHandler) HandleNotFound(w http.ResponseWriter, r *http.Request) {
	err := NewNotFoundError("endpoint")
	h.HandleError(w, r, err)
}

// HandleMethodNotAllowed handles 405 errors.
func (h *ErrorHandler) HandleMethodNotAllowed(w http.ResponseWriter, r *http.Request) {
	err := New(ErrorTypeValidation, "Method not allowed", http.StatusMethodNotAllowed)
	h.HandleError(w, r, err)
}

// HandlePanic handles panics in HTTP handlers.
func (h *ErrorHandler) HandlePanic(w http.ResponseWriter, r *http.Request, recovered interface{}) {
	h.logger.WithFields(logrus.Fields{
		"panic":     recovered,
		"method":    r.Method,
		"path":      r.URL.Path,
		"remote_ip": r.RemoteAddr,
		"trace_id":  r.Header.Get("X-Request-ID"),
	}).Error("Panic recovered in HTTP handler")

	err := NewInternalError("An unexpected error occurred")
	h.HandleError(w, r, err)
}

// writeJSON writes a JSON response.
func (h *ErrorHandler) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		h.logger.WithError(err).Error("Failed to encode error response")
	}
}

// Middleware returns an error handling middleware.
func (h *ErrorHandler) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				h.HandlePanic(w, r, recovered)
			}
		}()

		next.ServeHTTP(w, r)
	})
}
