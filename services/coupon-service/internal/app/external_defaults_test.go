package app

import (
	"context"
	"testing"

	"github.com/Medikong/services/services/coupon-service/internal/platform/config"
	"github.com/stretchr/testify/require"
)

func TestUnavailableExternalDependenciesAreLimitedToLocalEnvironments(t *testing.T) {
	for _, environment := range []string{"local", "development", "dev", "test", " DEV "} {
		t.Run(environment, func(t *testing.T) {
			require.NoError(t, allowUnavailableExternalDependencies(environment))
		})
	}
	for _, environment := range []string{"production", "staging", "private-dev", "aws-dev", ""} {
		t.Run(environment, func(t *testing.T) {
			require.Error(t, allowUnavailableExternalDependencies(environment))
		})
	}
}

func TestDefaultRuntimeCompositionRejectsUnavailableExternalDependenciesOutsideLocal(t *testing.T) {
	server, serverErr := NewServer(context.Background(), config.ServerConfig{
		Service: config.ServiceConfig{Environment: "production"},
	})
	require.Nil(t, server)
	require.Error(t, serverErr)
	require.Contains(t, serverErr.Error(), "external dependencies")

	worker, workerErr := NewWorker(context.Background(), config.WorkerConfig{
		Service: config.ServiceConfig{Environment: "production"},
	})
	require.Nil(t, worker)
	require.Error(t, workerErr)
	require.Contains(t, workerErr.Error(), "external dependencies")
}
