package app

import "testing"

func TestWorkerServiceNameKeepsSignalIdentityConsistent(t *testing.T) {
	if got := workerServiceName("auth-service"); got != "auth-service-worker" {
		t.Fatalf("worker service name = %q, want auth-service-worker", got)
	}
}
