package httpmiddleware

import "net/http"

type responseRecorder struct {
	http.ResponseWriter
	statusCode  int
	wroteHeader bool
}

func (r *responseRecorder) WriteHeader(statusCode int) {
	if r.wroteHeader {
		return
	}
	r.statusCode = statusCode
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(statusCode)
}

func (r *responseRecorder) Write(body []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(body)
}

func (r *responseRecorder) StatusCode() int {
	if r.statusCode == 0 {
		return http.StatusOK
	}
	return r.statusCode
}

func (r *responseRecorder) WroteHeader() bool {
	return r.wroteHeader
}

func ensureRecorder(w http.ResponseWriter) *responseRecorder {
	if recorder, ok := w.(*responseRecorder); ok {
		return recorder
	}
	return &responseRecorder{ResponseWriter: w, statusCode: http.StatusOK}
}
