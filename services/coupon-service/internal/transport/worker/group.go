package worker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/samber/oops"
)

type Job interface {
	RunOnce(context.Context) (int, error)
}

type NamedJob struct {
	Name string
	Job  Job
}

type State struct {
	mu          sync.RWMutex
	lastSuccess map[string]time.Time
	lastError   map[string]error
}

func NewState() *State {
	return &State{lastSuccess: make(map[string]time.Time), lastError: make(map[string]error)}
}

func (s *State) record(name string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.lastError[name] = err
		return
	}
	s.lastSuccess[name] = time.Now().UTC()
	delete(s.lastError, name)
}

func (s *State) Ready(context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for name, err := range s.lastError {
		return oops.In("coupon_worker").Code("coupon.worker_not_ready").With("job", name).Wrap(err)
	}
	return nil
}

type Group struct {
	jobs         []NamedJob
	pollInterval time.Duration
	state        *State
	log          *slog.Logger
}

func NewGroup(jobs []NamedJob, pollInterval time.Duration, state *State, log *slog.Logger) (*Group, error) {
	if len(jobs) == 0 || pollInterval <= 0 || state == nil || log == nil {
		return nil, oops.In("coupon_worker").Code("coupon.worker_group_invalid").New("worker group configuration is incomplete")
	}
	for _, job := range jobs {
		if job.Name == "" || job.Job == nil {
			return nil, oops.In("coupon_worker").Code("coupon.worker_job_invalid").New("worker job name and implementation are required")
		}
	}
	return &Group{jobs: jobs, pollInterval: pollInterval, state: state, log: log}, nil
}

func (g *Group) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := make(chan error, len(g.jobs))
	var wait sync.WaitGroup
	for _, named := range g.jobs {
		named := named
		wait.Add(1)
		go func() {
			defer wait.Done()
			errCh <- g.runJob(runCtx, named)
		}()
	}
	done := make(chan struct{})
	go func() {
		wait.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
		cancel()
		<-done
		for range g.jobs {
			if err := <-errCh; err != nil {
				return err
			}
		}
		return nil
	case err := <-errCh:
		cancel()
		<-done
		if err == nil && ctx.Err() == nil {
			return oops.In("coupon_worker").Code("coupon.worker_stopped_early").New("worker job stopped before shutdown")
		}
		return err
	}
}

func (g *Group) runJob(ctx context.Context, named NamedJob) error {
	ticker := time.NewTicker(g.pollInterval)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		count, err := named.Job.RunOnce(ctx)
		if err != nil && ctx.Err() != nil {
			return nil
		}
		g.state.record(named.Name, err)
		if err != nil {
			g.log.ErrorContext(ctx, "worker iteration failed", "worker", named.Name, "error", err.Error())
		} else if count > 0 {
			g.log.InfoContext(ctx, "worker batch completed", "worker", named.Name, "count", count)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
