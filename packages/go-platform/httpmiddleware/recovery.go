package httpmiddleware

import (
	"fmt"
	"net/http"

	"github.com/samber/oops"

	"github.com/Medikong/services/packages/go-platform/httpapi"
)

func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := newResponseRecorder(w)
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			err := recoveredError(recovered)
			if recorder.WroteHeader() {
				httpapi.ReportError(r.Context(), err, http.StatusInternalServerError, "common.internal")
				panic(recovered)
			}
			httpapi.WriteError(recorder.Writer(), r, err)
		}()
		next.ServeHTTP(recorder.Writer(), r)
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
