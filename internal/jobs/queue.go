// Package jobs runs lookups on a pool of worker goroutines.
package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/ilyasslefhaili/reachly/internal/finder"
)

type State string

const (
	Queued  State = "queued"
	Running State = "running"
	Done    State = "done"
	Failed  State = "failed"
)

type Job struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Company   string         `json:"company"`
	State     State          `json:"state"`
	Result    *finder.Result `json:"result,omitempty"`
	Error     string         `json:"error,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// Store is an interface so the in-memory store can be swapped for SQLite/Postgres in the cloud.
type Store interface {
	Put(Job)
	Get(id string) (Job, bool)
	Prune(olderThan time.Time)
}

type MemStore struct {
	mu   sync.RWMutex
	jobs map[string]Job
}

func NewMemStore() *MemStore { return &MemStore{jobs: map[string]Job{}} }

func (s *MemStore) Put(j Job) { s.mu.Lock(); s.jobs[j.ID] = j; s.mu.Unlock() }
func (s *MemStore) Get(id string) (Job, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	j, ok := s.jobs[id]
	return j, ok
}
func (s *MemStore) Prune(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, j := range s.jobs {
		if j.UpdatedAt.Before(t) && (j.State == Done || j.State == Failed) {
			delete(s.jobs, id)
		}
	}
}

var ErrQueueFull = errors.New("queue is full, try again shortly")

type Queue struct {
	store   Store
	finder  *finder.Finder
	ch      chan string
	timeout time.Duration
}

// New starts `workers` goroutines that pull job IDs from a buffered channel.
func New(ctx context.Context, store Store, f *finder.Finder, workers int) *Queue {
	q := &Queue{store: store, finder: f, ch: make(chan string, 10000), timeout: 90 * time.Second}
	for range workers {
		go q.worker(ctx)
	}
	go func() { // drop finished jobs after a day
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				store.Prune(time.Now().Add(-24 * time.Hour))
			}
		}
	}()
	return q
}

func (q *Queue) Submit(name, company string) (Job, error) {
	now := time.Now()
	j := Job{ID: newID(), Name: name, Company: company, State: Queued, CreatedAt: now, UpdatedAt: now}
	q.store.Put(j)
	select {
	case q.ch <- j.ID:
		return j, nil
	default:
		j.State, j.Error = Failed, ErrQueueFull.Error()
		q.store.Put(j)
		return j, ErrQueueFull
	}
}

func (q *Queue) Get(id string) (Job, bool) { return q.store.Get(id) }

func (q *Queue) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-q.ch:
			j, ok := q.store.Get(id)
			if !ok {
				continue
			}
			j.State, j.UpdatedAt = Running, time.Now()
			q.store.Put(j)

			jctx, cancel := context.WithTimeout(ctx, q.timeout)
			res, err := q.finder.Find(jctx, j.Name, j.Company)
			cancel()

			j.UpdatedAt = time.Now()
			if err != nil {
				j.State, j.Error = Failed, err.Error()
			} else {
				j.State, j.Result = Done, &res
			}
			q.store.Put(j)
		}
	}
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
