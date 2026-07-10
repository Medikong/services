module github.com/Medikong/services/packages/go-authz

go 1.26

require (
	github.com/Medikong/services/packages/go-contracts v0.0.0
	github.com/Medikong/services/packages/go-platform v0.0.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/oklog/ulid/v2 v2.1.1 // indirect
	github.com/samber/lo v1.53.0 // indirect
	github.com/samber/oops v1.22.0 // indirect
	go.opentelemetry.io/otel v1.44.0 // indirect
	go.opentelemetry.io/otel/trace v1.44.0 // indirect
	golang.org/x/text v0.38.0 // indirect
)

replace github.com/Medikong/services/packages/go-contracts => ../go-contracts

replace github.com/Medikong/services/packages/go-platform => ../go-platform
