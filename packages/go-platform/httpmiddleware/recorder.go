package httpmiddleware

import (
	"net/http"

	"github.com/felixge/httpsnoop"
)

type responseRecorder struct {
	writer      http.ResponseWriter
	statusCode  int
	wroteHeader bool
}

func newResponseRecorder(w http.ResponseWriter) *responseRecorder {
	recorder := &responseRecorder{statusCode: http.StatusOK}
	recorder.writer = httpsnoop.Wrap(w, httpsnoop.Hooks{
		Flush: func(next httpsnoop.FlushFunc) httpsnoop.FlushFunc {
			return func() {
				if !recorder.wroteHeader {
					recorder.statusCode = http.StatusOK
					recorder.wroteHeader = true
				}
				next()
			}
		},
		WriteHeader: func(next httpsnoop.WriteHeaderFunc) httpsnoop.WriteHeaderFunc {
			return func(statusCode int) {
				if recorder.wroteHeader {
					return
				}
				recorder.statusCode = statusCode
				recorder.wroteHeader = true
				next(statusCode)
			}
		},
		Write: func(next httpsnoop.WriteFunc) httpsnoop.WriteFunc {
			return func(body []byte) (int, error) {
				if !recorder.wroteHeader {
					recorder.statusCode = http.StatusOK
					recorder.wroteHeader = true
				}
				return next(body)
			}
		},
	})
	return recorder
}

func (r *responseRecorder) Writer() http.ResponseWriter {
	return r.writer
}

func (r *responseRecorder) StatusCode() int {
	return r.statusCode
}

func (r *responseRecorder) WroteHeader() bool {
	return r.wroteHeader
}
