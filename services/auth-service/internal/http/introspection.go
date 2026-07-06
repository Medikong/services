package http

import (
	nethttp "net/http"

	"github.com/Medikong/services/packages/go-contracts/headers"
	"github.com/Medikong/services/packages/go-platform/httpapi"
)

func (h Handler) Introspect(w nethttp.ResponseWriter, r *nethttp.Request) {
	result, err := h.services.Sessions.Introspect(r.Context(), r.Header.Get(headers.Authorization))
	if err != nil {
		httpapi.WriteError(w, r, mapAuthError(err))
		return
	}
	httpapi.WriteJSON(w, nethttp.StatusOK, map[string]any{"principal": result.Principal})
}
