package httpmiddleware

import (
	"fmt"
	"net/http"

	"github.com/samber/oops"

	"github.com/Medikong/services/packages/go-platform/httpapi"
	"github.com/Medikong/services/packages/go-platform/logger"
)

func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := ensureRecorder(w)
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			err := recoveredError(recovered)
			logger.Error(r.Context(), "http.request.panic", logger.Err(err))
			if recorder.WroteHeader() {
				panic(recovered)
			}
			httpapi.WriteError(recorder, r, httpapi.Internal(err))
		}()
		next.ServeHTTP(recorder, r)
	})
}

func recoveredError(recovered any) error {
	if err, ok := recovered.(error); ok {
		return oops.
			In("http_middleware").
			Code("common.panic").
			Public("요청 처리 중 오류가 발생했습니다.").
			Wrap(err)
	}
	return oops.
		In("http_middleware").
		Code("common.panic").
		Public("요청 처리 중 오류가 발생했습니다.").
		With("panic", fmt.Sprint(recovered)).
		New("panic recovered")
}
