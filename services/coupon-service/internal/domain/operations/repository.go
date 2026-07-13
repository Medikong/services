package operations

import (
	"context"
	"time"

	"github.com/Medikong/services/services/coupon-service/internal/domain/reliability"
)

type Repository interface {
	Create(context.Context, Control, reliability.Event, reliability.Command) (Control, error)
	Find(context.Context, string) (Control, error)
	FindEffective(context.Context, Scope, time.Time) ([]Control, error)
	ApplyNotice(context.Context, string, NoticeUpdate, reliability.Command) (Control, error)
}
