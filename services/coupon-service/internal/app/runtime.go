package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"

	"github.com/Medikong/services/services/coupon-service/internal/platform/migration"
)

func checkDatabase(ctx context.Context, db *pgxpool.Pool) error {
	if db == nil {
		return oops.In("coupon_runtime").Code("coupon.database_required").New("postgres pool is required")
	}
	if err := db.Ping(ctx); err != nil {
		return oops.In("coupon_runtime").Code("coupon.database_unavailable").Wrap(err)
	}
	return migration.CheckSchema(ctx, db)
}

func listen(addr, name string) (net.Listener, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, oops.In("coupon_runtime").Code("coupon.http_listen_failed").With("server", name).Wrap(err)
	}
	return listener, nil
}

func serveHTTP(server *http.Server, listener net.Listener, name string) error {
	err := server.Serve(listener)
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return oops.In("coupon_runtime").Code("coupon.http_serve_failed").With("server", name).Wrap(err)
}

func shutdownHTTP(server *http.Server, timeout time.Duration, name string) error {
	if server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		closeErr := server.Close()
		if closeErr != nil {
			closeErr = oops.In("coupon_runtime").Code("coupon.http_close_failed").With("server", name).Wrap(closeErr)
		}
		return oops.Join(
			oops.In("coupon_runtime").Code("coupon.http_shutdown_failed").With("server", name).Wrap(err),
			closeErr,
		)
	}
	return nil
}
