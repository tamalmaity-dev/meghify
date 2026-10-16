// Package jobs runs a durable background worker over a database-backed queue.
//
// The queue is a table rather than Redis: a self-hosted panel should not need a
// second service just to process deploys and backups. Workers claim rows with a
// guarded UPDATE, so running several panel replicas is safe without a lock.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/tamalmaity-dev/meghify/internal/db"
)

// Handler processes one job payload. Returning an error schedules a retry.
type Handler func(ctx context.Context, payload string) error

// Runner polls the job table and dispatches work.
type Runner struct {
	store       *db.Store
	handlers    map[string]Handler
	concurrency int
	pollEvery   time.Duration
	logger      *slog.Logger

	cron    *cron.Cron
	wg      sync.WaitGroup
	cancel  context.CancelFunc
	mu      sync.Mutex
	stopped bool
}

// Options configures the runner.
type Options struct {
	Store       *db.Store
	Concurrency int
	Logger      *slog.Logger
	// PollEvery bounds how often idle workers re-check the queue. Default 2s.
	PollEvery time.Duration
}

// NewRunner builds a worker pool.
func NewRunner(opts Options) *Runner {
	if opts.Concurrency <= 0 {
		opts.Concurrency = 2
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.PollEvery <= 0 {
		opts.PollEvery = 2 * time.Second
	}
	return &Runner{
		store:       opts.Store,
		handlers:    map[string]Handler{},
		concurrency: opts.Concurrency,
		pollEvery:   opts.PollEvery,
		logger:      opts.Logger,
		cron:        cron.New(cron.WithChain(cron.Recover(cronLogger{l: opts.Logger}))),
	}
}

// cronLogger adapts slog to the cron.Logger interface.
type cronLogger struct{ l *slog.Logger }

func (c cronLogger) Info(msg string, keysAndValues ...any) {
	c.l.Info(msg, keysAndValues...)
}

func (c cronLogger) Error(err error, msg string, keysAndValues ...any) {
	c.l.Error(msg, append(keysAndValues, "error", err)...)
}

func (c cronLogger) Printf(format string, v ...any) {
	c.l.Warn("scheduler", "detail", fmt.Sprintf(format, v...))
}

// Register binds a handler to a job kind. Later registrations replace earlier ones.
func (r *Runner) Register(kind string, h Handler) {
	r.handlers[kind] = h
}

// RegisterCron schedules a recurring job. The cron entry point enqueues the job
// through the normal queue, so a restart never loses a schedule: missed runs are
// simply picked up on the next start.
func (r *Runner) RegisterCron(spec, kind, payload string) {
	if r.cron == nil {
		return
	}
	if _, err := r.cron.AddFunc(spec, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, _, err := r.store.Jobs.EnqueueUnique(ctx, kind, payload, time.Now(), 1); err != nil {
			r.logger.Error("schedule job failed", "kind", kind, "error", err)
		}
	}); err != nil {
		r.logger.Error("register cron failed", "kind", kind, "spec", spec, "error", err)
	}
}

// StartCronScheduler starts the cron sidecar. Call it once, before Start.
func (r *Runner) StartCronScheduler() {
	if r.cron != nil {
		r.cron.Start()
	}
}

// Start launches the worker pool. Calling Start twice is a no-op.
func (r *Runner) Start(ctx context.Context) {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	r.mu.Unlock()

	for i := 0; i < r.concurrency; i++ {
		r.wg.Add(1)
		go r.worker(runCtx, i)
	}
	r.logger.Info("job runner started", "workers", r.concurrency)
}

// Stop waits for in-flight jobs to finish.
func (r *Runner) Stop() {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return
	}
	r.stopped = true
	if r.cron != nil {
		r.cron.Stop()
	}
	if r.cancel != nil {
		r.cancel()
	}
	r.mu.Unlock()
	r.wg.Wait()
}

// Pending reports how many jobs are waiting, used by the dashboard.
func (r *Runner) Pending() int {
	n, err := r.store.Jobs.PendingCount(context.Background())
	if err != nil {
		return 0
	}
	return n
}

func (r *Runner) worker(ctx context.Context, id int) {
	defer r.wg.Done()
	log := r.logger.With("worker", id)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		job, err := r.store.Jobs.Claim(ctx)
		if err != nil {
			if ctx.Err() == nil {
				log.Error("claim job failed", "error", err)
			}
			if !sleep(ctx, r.pollEvery) {
				return
			}
			continue
		}
		if job == nil {
			if !sleep(ctx, r.pollEvery) {
				return
			}
			continue
		}

		r.process(ctx, log, job)
	}
}

func (r *Runner) process(ctx context.Context, log *slog.Logger, job *db.Job) {
	handler, ok := r.handlers[job.Kind]
	if !ok {
		// Dead-letter unknown kinds rather than retrying forever.
		r.store.Jobs.Fail(ctx, job.ID, "no handler registered for kind "+job.Kind)
		log.Warn("unknown job kind", "kind", job.Kind)
		return
	}

	started := time.Now()
	log.Info("job started", "kind", job.Kind, "job", job.ID, "attempt", job.Attempts)

	// A panic in a handler must not take down the worker, so it becomes a
	// failure and the job follows the normal retry path.
	var err error
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				err = fmt.Errorf("panic: %v\n%s", rec, debug.Stack())
			}
		}()
		err = handler(ctx, job.Payload)
	}()

	elapsed := time.Since(started)
	if err != nil {
		r.store.Jobs.Fail(ctx, job.ID, err.Error())
		log.Error("job failed", "kind", job.Kind, "job", job.ID,
			"attempt", job.Attempts, "elapsed", elapsed, "error", err)
		return
	}

	if err := r.store.Jobs.Complete(ctx, job.ID); err != nil && !errors.Is(err, db.ErrNotFound) {
		log.Error("complete job failed", "job", job.ID, "error", err)
	}
	log.Info("job finished", "kind", job.Kind, "job", job.ID, "elapsed", elapsed)
}

// Enqueue is a convenience wrapper used by handlers outside the API layer.
func (r *Runner) Enqueue(ctx context.Context, kind, payload string, delay time.Duration, maxAttempts int) (*db.Job, error) {
	return r.store.Jobs.Enqueue(ctx, kind, payload, time.Now().Add(delay), maxAttempts)
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
