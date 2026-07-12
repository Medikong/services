package controller

import (
	"net/http"

	"github.com/Medikong/services/services/auth-service/internal/application"
	httpcontract "github.com/Medikong/services/services/auth-service/internal/transport/httpcontract"
)

func writeApplicationError(w http.ResponseWriter, r *http.Request, err error) {
	appError := application.AsError(err)
	title := "요청을 처리할 수 없습니다."
	if appError.Status == http.StatusServiceUnavailable {
		title = "인증 서비스를 일시적으로 사용할 수 없습니다."
	}
	httpcontract.WriteProblem(w, r, httpcontract.NewContractError(
		appError.Status,
		appError.Code,
		title,
		appError.Detail,
		appError.Retryable,
	))
}
