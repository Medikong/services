package handler

import "github.com/Medikong/services/packages/go-platform/operational"

func operationalHandler() operational.Handler {
	return operational.New(serviceName, nil)
}
