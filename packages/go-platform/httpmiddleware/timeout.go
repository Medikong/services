package httpmiddleware

import (
	"context"
	"net/http"
	"time"

	"github.com/Medikong/services/packages/go-platform/httpapi"
)

func Timeout(timeout time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			recorder := newResponseRecorder(w)
			defer func() {
				timedOut := ctx.Err() == context.DeadlineExceeded
				cancel()
				if timedOut && !recorder.WroteHeader() {
					httpapi.WriteError(recorder.Writer(), r.WithContext(ctx), httpapi.GatewayTimeout(
						"common.timeout",
						"요청 처리 시간이 초과되었습니다.",
					))
				}
			}()
			next.ServeHTTP(recorder.Writer(), r.WithContext(ctx))
		})
	}
}
