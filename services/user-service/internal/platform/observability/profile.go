package observability

import (
	"github.com/grafana/pyroscope-go"
	"github.com/samber/oops"

	"github.com/Medikong/services/services/user-service/internal/platform/config"
)

func StartProfiler(service config.ServiceConfig, profile config.ProfileConfig) (*pyroscope.Profiler, error) {
	if !profile.PyroscopeEnabled {
		return nil, nil
	}
	profiler, err := pyroscope.Start(pyroscope.Config{
		ApplicationName:   service.Name,
		ServerAddress:     profile.PyroscopeAddress,
		BasicAuthUser:     profile.PyroscopeUser,
		BasicAuthPassword: profile.PyroscopePassword,
		TenantID:          profile.PyroscopeTenantID,
		Tags: map[string]string{
			"service":     service.Name,
			"environment": service.Environment,
			"version":     service.Version,
		},
		ProfileTypes: []pyroscope.ProfileType{
			pyroscope.ProfileCPU,
			pyroscope.ProfileInuseObjects,
			pyroscope.ProfileAllocObjects,
			pyroscope.ProfileInuseSpace,
			pyroscope.ProfileAllocSpace,
		},
	})
	if err != nil {
		return nil, oops.In("user_profile").Code("profile.start_failed").Wrap(err)
	}
	return profiler, nil
}
