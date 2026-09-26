package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// JobStatus is the lifecycle state of a headless agent job.
type JobStatus string

const (
	StatusQueued    JobStatus = "queued"
	StatusRunning   JobStatus = "running"
	StatusSucceeded JobStatus = "succeeded"
	StatusFailed    JobStatus = "failed"
	StatusCancelled JobStatus = "cancelled"
)

// Terminal reports whether the status is final.
func (s JobStatus) Terminal() bool {
	return s == StatusSucceeded || s == StatusFailed || s == StatusCancelled
}

// MaxOutputBytes caps the persisted combined output per job (keep the tail:
// final summaries live at the end of a run).
const MaxOutputBytes = 256 << 10 // 256 KiB

// Job is one headless agent run.
type Job struct {
	ID         string    `json:"id"`
	User       string    `json:"user"`
	Workspace  string    `json:"workspace"`
	Prompt     string    `json:"prompt"`
	Status     JobStatus `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	ExitCode   int       `json:"exit_code,omitempty"`
	Output     string    `json:"output,omitempty"`
	Err        string    `json:"error,omitempty"`
}

// ErrJobNotFound is returned by Get for unknown IDs.
var ErrJobNotFound = errors.New("platform: job not found")

// JobStore persists one JSON file per job under a jobs directory. File-per-job
// keeps reads cheap for status polling and survives server restarts.
type JobStore struct {
	mu  sync.Mutex
	dir string
}

// NewJobStore creates (if needed) the jobs directory.
func NewJobStore(dir string) (*JobStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("platform: jobs dir: %w", err)
	}
	return &JobStore{dir: dir}, nil
}

// Create registers a new queued job and persists it.
func (s *JobStore) Create(user, workspace, prompt string) (*Job, error) {
	id, err := RandomID()
	if err != nil {
		return nil, err
	}
	job := &Job{
		ID: id, User: user, Workspace: workspace, Prompt: prompt,
		Status: StatusQueued, CreatedAt: time.Now().UTC(),
	}
	if err := s.persist(job); err != nil {
		return nil, err
	}
	return job, nil
}

// Get loads a job by ID.
func (s *JobStore) Get(id string) (*Job, error) {
	if !validJobID(id) {
		return nil, ErrJobNotFound
	}
	b, err := os.ReadFile(s.file(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrJobNotFound
		}
		return nil, fmt.Errorf("platform: read job: %w", err)
	}
	var job Job
	if err := json.Unmarshal(b, &job); err != nil {
		return nil, fmt.Errorf("platform: parse job %s: %w", id, err)
	}
	return &job, nil
}

// Update persists the current state of a job.
func (s *JobStore) Update(job *Job) error {
	return s.persist(job)
}

// List returns all jobs, newest first.
func (s *JobStore) List() ([]*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("platform: list jobs: %w", err)
	}
	var jobs []*Job
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue // concurrent delete/unreadable: skip
		}
		var job Job
		if json.Unmarshal(b, &job) != nil {
			continue
		}
		jobs = append(jobs, &job)
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].CreatedAt.After(jobs[j].CreatedAt) })
	return jobs, nil
}

func (s *JobStore) persist(job *Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.file(job.ID), b, 0o600); err != nil {
		return fmt.Errorf("platform: write job: %w", err)
	}
	return nil
}

func (s *JobStore) file(id string) string {
	return filepath.Join(s.dir, "job-"+id+".json")
}

// validJobID guards against path traversal via crafted IDs.
func validJobID(id string) bool {
	if len(id) != 16 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
