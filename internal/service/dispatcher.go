package service

import (
	"context"
	"log"
	"sync"
	"time"

	"zephyr/internal/model"
)

type JobHandler func(ctx context.Context, job *model.Job) error

type jobTypeConfig struct {
	concurrency int
	handler     JobHandler
	semaphore   chan struct{}
}

type Dispatcher struct {
	types   map[string]*jobTypeConfig
	cancels sync.Map // jobID -> context.CancelFunc
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

func NewDispatcher() *Dispatcher {
	return &Dispatcher{
		types:  make(map[string]*jobTypeConfig),
		stopCh: make(chan struct{}),
	}
}

func (d *Dispatcher) Register(jobType string, concurrency int, handler JobHandler) {
	d.types[jobType] = &jobTypeConfig{
		concurrency: concurrency,
		handler:     handler,
		semaphore:   make(chan struct{}, concurrency),
	}
}

func (d *Dispatcher) Start() {
	// Reset any jobs that were running when the server stopped
	if err := model.ResetRunningJobs(); err != nil {
		log.Printf("[dispatcher] failed to reset running jobs: %v", err)
	}

	d.wg.Add(1)
	go d.poll()
}

func (d *Dispatcher) Stop() {
	close(d.stopCh)

	// Cancel all running jobs
	d.cancels.Range(func(key, value interface{}) bool {
		if cancel, ok := value.(context.CancelFunc); ok {
			cancel()
		}
		return true
	})

	d.wg.Wait()
}

func (d *Dispatcher) Cancel(jobID string) {
	if cancel, ok := d.cancels.LoadAndDelete(jobID); ok {
		if fn, ok := cancel.(context.CancelFunc); ok {
			fn()
		}
	}
	_ = model.UpdateJobStatus(jobID, "aborted")
}

func (d *Dispatcher) poll() {
	defer d.wg.Done()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.stopCh:
			return
		case <-ticker.C:
			d.tryDispatch()
		}
	}
}

func (d *Dispatcher) tryDispatch() {
	for jobType, cfg := range d.types {
		// Non-blocking check if there's capacity
		select {
		case cfg.semaphore <- struct{}{}:
			// Got a slot, try to claim a job
			job, err := model.ClaimPendingJob(jobType)
			if err != nil {
				log.Printf("[dispatcher] claim error for %s: %v", jobType, err)
				<-cfg.semaphore
				continue
			}
			if job == nil {
				// No pending jobs of this type
				<-cfg.semaphore
				continue
			}

			ctx, cancel := context.WithCancel(context.Background())
			d.cancels.Store(job.JobID, cancel)

			d.wg.Add(1)
			go func(j *model.Job, tc *jobTypeConfig) {
				defer d.wg.Done()
				defer func() { <-tc.semaphore }()
				defer d.cancels.Delete(j.JobID)

				log.Printf("[dispatcher] starting job %s (type=%s)", j.JobID, j.Type)

				if err := tc.handler(ctx, j); err != nil {
					if ctx.Err() != nil {
						log.Printf("[dispatcher] job %s cancelled", j.JobID)
						_ = model.UpdateJobStatus(j.JobID, "aborted")
					} else {
						log.Printf("[dispatcher] job %s failed: %v", j.JobID, err)
						_ = model.UpdateJobError(j.JobID, err.Error())
					}
				} else {
					log.Printf("[dispatcher] job %s completed", j.JobID)
				}
			}(job, cfg)
		default:
			// All slots busy for this type
		}
	}
}
