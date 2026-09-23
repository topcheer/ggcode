package cron

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// --- sa-104 coverage net: scheduler lifecycle, fireJob matrix, patrol,
// persistence edges, migration branches, parser parts. Zero production-code
// changes; all tests are deterministic (no long sleeps, direct fireJob /
// patrolCheck invocation, every scheduler Shut down via defer). ---

// sa104Rec records enqueue callbacks synchronously (fireJob calls enqueue
// inline, so no async waits are needed).
type sa104Rec struct {
	mu      sync.Mutex
	prompts []string
}

func (r *sa104Rec) fn() func(string, bool) {
	return func(p string, _ bool) {
		r.mu.Lock()
		r.prompts = append(r.prompts, p)
		r.mu.Unlock()
	}
}

func (r *sa104Rec) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.prompts)
}

// sa104BrokenStorePath returns a store path whose parent is a regular file,
// so MkdirAll/AtomicWriteFile deterministically fail on every platform.
func sa104BrokenStorePath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(blocker, "sub", "store.json")
}

func sa104InsertJob(s *Scheduler, j *Job) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[j.ID] = j
	return s.generations[j.ID]
}

// --- Create ---

func TestSa104_Create_RejectsShortInterval(t *testing.T) {
	s := NewScheduler(nil, "")
	defer s.Shutdown()
	// */4 fires every 4 minutes < minCronIntervalMinutes (5).
	if _, err := s.Create("*/4 * * * *", "p", true, false); err == nil {
		t.Fatal("expected interval-too-short error, got nil")
	}
	if len(s.List()) != 0 {
		t.Fatal("rejected job must not be stored")
	}
}

func TestSa104_Create_RollbackOnSaveFailure(t *testing.T) {
	s := NewScheduler(nil, sa104BrokenStorePath(t))
	defer s.Shutdown()
	job, err := s.Create("*/7 * * * *", "p", true, false)
	if err == nil {
		t.Fatal("expected save failure error")
	}
	if job.ID != "" {
		t.Fatalf("expected zero Job on failure, got %+v", job)
	}
	if _, ok := s.Get(""); ok {
		t.Fatal("no job should be registered")
	}
	s.mu.Lock()
	n := len(s.jobs)
	s.mu.Unlock()
	if n != 0 {
		t.Fatalf("rollback must remove the job, %d remain", n)
	}
}

// --- Delete / DeleteWithError ---

func TestSa104_DeleteWithError_NotFound(t *testing.T) {
	s := NewScheduler(nil, "")
	defer s.Shutdown()
	deleted, err := s.DeleteWithError("nope")
	if deleted || err != nil {
		t.Fatalf("not-found must be (false, nil), got (%t, %v)", deleted, err)
	}
	if s.Delete("nope") {
		t.Fatal("Delete of missing job must be false")
	}
}

func TestSa104_DeleteWithError_RollbackOnSaveFailure(t *testing.T) {
	rec := &sa104Rec{}
	s := NewScheduler(rec.fn(), "")
	defer s.Shutdown()
	job, err := s.Create("*/7 * * * *", "p", true, false)
	if err != nil {
		t.Fatalf("setup create: %v", err)
	}
	s.storePath = sa104BrokenStorePath(t)
	deleted, err := s.DeleteWithError(job.ID)
	if !deleted || err == nil {
		t.Fatalf("expected (true, err), got (%t, %v)", deleted, err)
	}
	if _, ok := s.Get(job.ID); !ok {
		t.Fatal("save failure must restore the job")
	}
}

// --- Update ---

func TestSa104_Update_NotFoundAndInvalidExpr(t *testing.T) {
	s := NewScheduler(nil, "")
	defer s.Shutdown()
	if _, err := s.Update("nope", nil, nil, nil); err == nil {
		t.Fatal("update of missing job must fail")
	}
	job, err := s.Create("*/7 * * * *", "p", true, false)
	if err != nil {
		t.Fatal(err)
	}
	bogus := "not a cron"
	if _, err := s.Update(job.ID, &bogus, nil, nil); err == nil {
		t.Fatal("invalid expression must fail")
	}
	got, _ := s.Get(job.ID)
	if got.CronExpr != "*/7 * * * *" {
		t.Fatalf("failed update must not mutate expr, got %q", got.CronExpr)
	}
	short := "*/4 * * * *"
	if _, err := s.Update(job.ID, &short, nil, nil); err == nil {
		t.Fatal("short interval must fail on update too")
	}
}

func TestSa104_Update_PromptAndQueueIfBusy(t *testing.T) {
	s := NewScheduler(nil, "")
	defer s.Shutdown()
	job, err := s.Create("*/7 * * * *", "old", true, false)
	if err != nil {
		t.Fatal(err)
	}
	prompt := "new prompt"
	q := true
	got, err := s.Update(job.ID, nil, &prompt, &q)
	if err != nil {
		t.Fatal(err)
	}
	if got.Prompt != "new prompt" || !got.QueueIfBusy {
		t.Fatalf("fields not updated: %+v", got)
	}
}

func TestSa104_Update_PausedKeepsZeroNextFire(t *testing.T) {
	s := NewScheduler(nil, "")
	defer s.Shutdown()
	job, err := s.Create("*/7 * * * *", "p", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Pause(job.ID); err != nil {
		t.Fatal(err)
	}
	expr := "*/11 * * * *"
	got, err := s.Update(job.ID, &expr, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Paused {
		t.Fatal("paused state must survive update")
	}
	if !got.NextFire.IsZero() {
		t.Fatalf("paused job must keep zero NextFire (#667), got %v", got.NextFire)
	}
	if got.CronExpr != expr {
		t.Fatalf("expr not applied: %q", got.CronExpr)
	}
}

func TestSa104_Update_RollbackOnSaveFailure_Active(t *testing.T) {
	s := NewScheduler(nil, "")
	defer s.Shutdown()
	job, err := s.Create("*/7 * * * *", "orig", true, false)
	if err != nil {
		t.Fatal(err)
	}
	origNext := job.NextFire
	s.storePath = sa104BrokenStorePath(t)
	expr := "*/13 * * * *"
	if _, err := s.Update(job.ID, &expr, nil, nil); err == nil {
		t.Fatal("expected save failure")
	}
	got, _ := s.Get(job.ID)
	if got.CronExpr != "*/7 * * * *" || got.Prompt != "orig" {
		t.Fatalf("rollback failed: %+v", got)
	}
	if got.NextFire.IsZero() {
		t.Fatal("rollback must restore a future NextFire for active jobs")
	}
	if got.NextFire.Before(origNext.Add(-time.Minute)) {
		t.Fatalf("NextFire not preserved: %v vs %v", got.NextFire, origNext)
	}
}

func TestSa104_Update_RollbackOnSaveFailure_Paused(t *testing.T) {
	s := NewScheduler(nil, "")
	defer s.Shutdown()
	job, err := s.Create("*/7 * * * *", "p", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Pause(job.ID); err != nil {
		t.Fatal(err)
	}
	s.storePath = sa104BrokenStorePath(t)
	expr := "*/13 * * * *"
	if _, err := s.Update(job.ID, &expr, nil, nil); err == nil {
		t.Fatal("expected save failure")
	}
	got, _ := s.Get(job.ID)
	if !got.Paused {
		t.Fatal("paused rollback must stay paused")
	}
	if !got.NextFire.IsZero() {
		t.Fatalf("paused rollback must keep zero NextFire, got %v", got.NextFire)
	}
}

// --- Pause / Resume ---

func TestSa104_Pause_NotFoundAndIdempotent(t *testing.T) {
	s := NewScheduler(nil, "")
	defer s.Shutdown()
	if err := s.Pause("nope"); err == nil {
		t.Fatal("pause of missing job must fail")
	}
	job, err := s.Create("*/7 * * * *", "p", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Pause(job.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Pause(job.ID); err != nil {
		t.Fatalf("second pause must be a no-op, got %v", err)
	}
	got, _ := s.Get(job.ID)
	if !got.Paused || !got.NextFire.IsZero() {
		t.Fatalf("pause invariants violated: %+v", got)
	}
}

func TestSa104_Pause_RollbackOnSaveFailure(t *testing.T) {
	s := NewScheduler(nil, "")
	defer s.Shutdown()
	job, err := s.Create("*/7 * * * *", "p", true, false)
	if err != nil {
		t.Fatal(err)
	}
	s.storePath = sa104BrokenStorePath(t)
	if err := s.Pause(job.ID); err == nil {
		t.Fatal("expected save failure")
	}
	got, _ := s.Get(job.ID)
	if got.Paused {
		t.Fatal("rollback must un-pause")
	}
	if got.NextFire.IsZero() {
		t.Fatal("rollback must restore NextFire")
	}
}

func TestSa104_Resume_NotFoundAndIdempotent(t *testing.T) {
	s := NewScheduler(nil, "")
	defer s.Shutdown()
	if err := s.Resume("nope"); err == nil {
		t.Fatal("resume of missing job must fail")
	}
	job, err := s.Create("*/7 * * * *", "p", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Resume(job.ID); err != nil {
		t.Fatalf("resume of active job must be a no-op, got %v", err)
	}
}

func TestSa104_Resume_RecomputesNextFire(t *testing.T) {
	s := NewScheduler(nil, "")
	defer s.Shutdown()
	job, err := s.Create("*/7 * * * *", "p", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Pause(job.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Resume(job.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(job.ID)
	if got.Paused || got.NextFire.IsZero() || !got.NextFire.After(time.Now().Add(-time.Minute)) {
		t.Fatalf("resume must recompute a future NextFire: %+v", got)
	}
}

func TestSa104_Resume_BrokenExprRollback(t *testing.T) {
	s := NewScheduler(nil, "")
	defer s.Shutdown()
	s.mu.Lock()
	s.jobs["cron-1-x"] = &Job{ID: "cron-1-x", CronExpr: "totally bogus", Prompt: "p", Recurring: true, Paused: true}
	s.mu.Unlock()
	if err := s.Resume("cron-1-x"); err == nil {
		t.Fatal("resume with broken expr must fail")
	}
	got, _ := s.Get("cron-1-x")
	if !got.Paused {
		t.Fatal("rollback must restore paused state")
	}
}

func TestSa104_Resume_RollbackOnSaveFailure(t *testing.T) {
	s := NewScheduler(nil, "")
	defer s.Shutdown()
	job, err := s.Create("*/7 * * * *", "p", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Pause(job.ID); err != nil {
		t.Fatal(err)
	}
	s.storePath = sa104BrokenStorePath(t)
	if err := s.Resume(job.ID); err == nil {
		t.Fatal("expected save failure")
	}
	got, _ := s.Get(job.ID)
	if !got.Paused || !got.NextFire.IsZero() {
		t.Fatalf("resume rollback must restore paused invariants: %+v", got)
	}
}

// --- SetSession ---

func TestSa104_SetSession_EmptyAndAlreadyBound(t *testing.T) {
	s := NewScheduler(nil, "")
	defer s.Shutdown()
	s.SetSession("", "old", "ws") // no-op
	if s.storePath != "" {
		t.Fatalf("empty storePath must be ignored, got %q", s.storePath)
	}
	p1 := filepath.Join(t.TempDir(), "a.json")
	p2 := filepath.Join(t.TempDir(), "b.json")
	s2 := NewScheduler(nil, p1)
	defer s2.Shutdown()
	s2.SetSession(p2, "", "")
	if s2.storePath != p1 {
		t.Fatalf("SetSession must be one-time only: bound=%q want=%q", s2.storePath, p1)
	}
}

func TestSa104_SetSession_MigratesAndLoads(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	oldPath := filepath.Join(dir, "cron-jobs.json")
	newPath := filepath.Join(dir, "session", "cron.json")

	old := oldStoreFile{
		workspaceKey(ws): workspaceBucket{
			Workspace: ws,
			Jobs: []jobJSON{
				{ID: "cron-1-legacy", CronExpr: "*/7 * * * *", Prompt: "legacy", Recurring: true, CreatedAt: time.Now().Format(time.RFC3339)},
				{ID: "cron-2-oneshot", CronExpr: "*/7 * * * *", Prompt: "gone", Recurring: false, CreatedAt: time.Now().Format(time.RFC3339)},
			},
		},
	}
	data, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	s := NewScheduler(nil, "")
	defer s.Shutdown()
	s.SetSession(newPath, oldPath, ws)

	jobs := s.List()
	if len(jobs) != 1 || jobs[0].Prompt != "legacy" {
		t.Fatalf("expected exactly the migrated recurring job, got %+v", jobs)
	}
	// One-shots are not migrated; bucket is consumed.
	var after oldStoreFile
	raw, err := os.ReadFile(oldPath)
	if err == nil && json.Unmarshal(raw, &after) == nil {
		if _, exists := after[workspaceKey(ws)]; exists {
			t.Fatal("migrated bucket must be removed from the old store")
		}
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("new session store must exist: %v", err)
	}
}

// --- Load ---

func sa104WriteStore(t *testing.T, path string, jobs []jobJSON) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(sessionStore{Jobs: jobs})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestSa104_Load_Edges(t *testing.T) {
	// Empty storePath: no-op.
	s := NewScheduler(nil, "")
	s.storePath = ""
	s.Load()
	defer s.Shutdown()

	// Missing file: no-op.
	dir := t.TempDir()
	s2 := NewScheduler(nil, filepath.Join(dir, "missing.json"))
	defer s2.Shutdown()
	s2.Load()
	if len(s2.List()) != 0 {
		t.Fatal("missing store must load nothing")
	}

	// Corrupted file: skip.
	corrupt := filepath.Join(dir, "corrupt.json")
	if err := os.WriteFile(corrupt, []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}
	s3 := NewScheduler(nil, corrupt)
	defer s3.Shutdown()
	s3.Load()
	if len(s3.List()) != 0 {
		t.Fatal("corrupt store must load nothing")
	}
}

func TestSa104_Load_SkipsOneShotsBrokenAndDuplicates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cron.json")
	ca := time.Now().Add(-time.Hour).Format(time.RFC3339)
	sa104WriteStore(t, path, []jobJSON{
		{ID: "cron-1-a", CronExpr: "*/7 * * * *", Prompt: "keep", Recurring: true, CreatedAt: ca},
		{ID: "cron-2-b", CronExpr: "*/9 * * * *", Prompt: "oneshot", Recurring: false, CreatedAt: ca},
		{ID: "cron-3-c", CronExpr: "broken expr", Prompt: "broken", Recurring: true, CreatedAt: ca},
	})
	s := NewScheduler(nil, path)
	defer s.Shutdown()
	s.Load()
	s.Load() // duplicate load must not double-register
	jobs := s.List()
	if len(jobs) != 1 || jobs[0].Prompt != "keep" {
		t.Fatalf("expected only the valid recurring job, got %+v", jobs)
	}
}

func TestSa104_Load_PausedZeroNextFireAndMaxID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cron.json")
	ca := time.Now().Add(-2 * time.Hour).Format(time.RFC3339)
	sa104WriteStore(t, path, []jobJSON{
		{ID: "cron-9-high", CronExpr: "*/7 * * * *", Prompt: "high", Recurring: true, CreatedAt: ca},
		{ID: "cron-4-paused", CronExpr: "*/7 * * * *", Prompt: "paused", Recurring: true, Paused: true, CreatedAt: ca},
	})
	s := NewScheduler(nil, path)
	defer s.Shutdown()
	s.Load()
	got, ok := s.Get("cron-4-paused")
	if !ok || !got.Paused || !got.NextFire.IsZero() {
		t.Fatalf("paused job must load with zero NextFire: %+v ok=%t", got, ok)
	}
	// New job IDs must not collide with the high loaded ID.
	job, err := s.Create("*/11 * * * *", "fresh", true, false)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if _, err := fmt.Sscanf(job.ID, "cron-%d", &n); err != nil || n <= 9 {
		t.Fatalf("new ID must exceed loaded max (9), got %q", job.ID)
	}
}

// --- save edges ---

func TestSa104_Save_EmptyPathAndRemovesEmptyStore(t *testing.T) {
	s := NewScheduler(nil, "")
	defer s.Shutdown()
	if err := s.save(); err != nil {
		t.Fatalf("save with empty path must be nil, got %v", err)
	}
	path := filepath.Join(t.TempDir(), "cron.json")
	if err := os.WriteFile(path, []byte(`{"jobs":[]}`), 0644); err != nil {
		t.Fatal(err)
	}
	s.storePath = path
	if err := s.save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("save with no jobs must remove the store file")
	}
}

func TestSa104_Save_PreservesForeignJobs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cron.json")
	foreign := sessionStore{Jobs: []jobJSON{{ID: "cron-9-foreign", CronExpr: "*/7 * * * *", Prompt: "foreign", Recurring: true, CreatedAt: time.Now().Format(time.RFC3339)}}}
	sa104WriteStore(t, path, foreign.Jobs)

	s := NewScheduler(nil, path)
	defer s.Shutdown()
	// Another process created a job this process has never seen.
	if _, err := s.Create("*/11 * * * *", "mine", true, false); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var ss sessionStore
	if err := json.Unmarshal(raw, &ss); err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, j := range ss.Jobs {
		ids[j.ID] = true
	}
	if !ids["cron-9-foreign"] {
		t.Fatalf("foreign job must survive our save, got %v", ids)
	}
}

// --- fireJob matrix (direct invocation; enqueue is synchronous) ---

func TestSa104_FireJob_StaleGenAndDeletedJob(t *testing.T) {
	rec := &sa104Rec{}
	s := NewScheduler(rec.fn(), "")
	defer s.Shutdown()

	j := &Job{ID: "cron-1-s", CronExpr: "*/7 * * * *", Prompt: "p", Recurring: true, NextFire: time.Now().Add(-time.Hour)}
	gen := sa104InsertJob(s, j)

	s.fireJob(j, gen+1) // stale generation
	if rec.count() != 0 {
		t.Fatal("stale generation must not enqueue")
	}

	s.mu.Lock()
	delete(s.jobs, j.ID)
	s.mu.Unlock()
	s.fireJob(j, gen) // job removed from map
	if rec.count() != 0 {
		t.Fatal("deleted job must not enqueue")
	}
}

func TestSa104_FireJob_Debounce(t *testing.T) {
	rec := &sa104Rec{}
	s := NewScheduler(rec.fn(), "")
	defer s.Shutdown()

	recj := &Job{ID: "cron-1-r", CronExpr: "*/7 * * * *", Prompt: "r", Recurring: true, NextFire: time.Now().Add(-time.Hour)}
	beforeNext := recj.NextFire
	gen := sa104InsertJob(s, recj)
	s.mu.Lock()
	s.lastEnqueue[recj.ID] = time.Now()
	s.mu.Unlock()

	s.fireJob(recj, gen)
	if rec.count() != 0 {
		t.Fatal("debounced fire must not enqueue")
	}
	got, _ := s.Get(recj.ID)
	if got.NextFire.IsZero() || !got.NextFire.After(beforeNext) {
		t.Fatalf("debounced recurring fire must advance NextFire (#519), got %v", got.NextFire)
	}

	one := &Job{ID: "cron-2-o", CronExpr: "*/7 * * * *", Prompt: "o", NextFire: time.Now().Add(-time.Hour)}
	ogen := sa104InsertJob(s, one)
	s.mu.Lock()
	s.lastEnqueue[one.ID] = time.Now()
	s.mu.Unlock()
	s.fireJob(one, ogen)
	if rec.count() != 0 {
		t.Fatal("debounced one-shot must not enqueue")
	}
	if _, ok := s.Get(one.ID); !ok {
		t.Fatal("debounced duplicate one-shot fire must leave the owner path in charge")
	}
}

func TestSa104_FireJob_NormalRecurringAndOneShot(t *testing.T) {
	rec := &sa104Rec{}
	s := NewScheduler(rec.fn(), "")
	defer s.Shutdown()

	recj := &Job{ID: "cron-1-r", CronExpr: "*/7 * * * *", Prompt: "recurring prompt", Recurring: true, NextFire: time.Now().Add(-time.Hour)}
	beforeNext := recj.NextFire
	gen := sa104InsertJob(s, recj)
	s.fireJob(recj, gen)
	if rec.count() != 1 {
		t.Fatalf("recurring fire must enqueue once, got %d", rec.count())
	}
	got, ok := s.Get(recj.ID)
	if !ok {
		t.Fatal("recurring job must survive its fire")
	}
	if !got.NextFire.After(beforeNext) {
		t.Fatalf("NextFire must advance past the consumed slot: %v", got.NextFire)
	}

	one := &Job{ID: "cron-2-o", CronExpr: "*/7 * * * *", Prompt: "one shot", NextFire: time.Now().Add(-time.Hour)}
	ogen := sa104InsertJob(s, one)
	s.fireJob(one, ogen)
	if rec.count() != 2 {
		t.Fatalf("one-shot fire must enqueue, got %d", rec.count())
	}
	if _, ok := s.Get(one.ID); ok {
		t.Fatal("one-shot job must be deleted after its fire")
	}
}

func TestSa104_FireJob_BrokenExprRemovesJob(t *testing.T) {
	dir := t.TempDir()
	rec := &sa104Rec{}
	s := NewScheduler(rec.fn(), filepath.Join(dir, "cron.json"))
	defer s.Shutdown()

	j := &Job{ID: "cron-1-b", CronExpr: "totally bogus", Prompt: "p", Recurring: true, NextFire: time.Now().Add(-time.Hour)}
	gen := sa104InsertJob(s, j)
	s.fireJob(j, gen)
	if _, ok := s.Get(j.ID); ok {
		t.Fatal("broken-expression job must be removed after fire")
	}
	if rec.count() != 1 {
		t.Fatalf("enqueue still happens before the re-check, got %d", rec.count())
	}

	// Debounced branch with a broken expression: removed without enqueue.
	j2 := &Job{ID: "cron-2-b", CronExpr: "still bogus", Prompt: "p", Recurring: true, NextFire: time.Now().Add(-time.Hour)}
	gen2 := sa104InsertJob(s, j2)
	s.mu.Lock()
	s.lastEnqueue[j2.ID] = time.Now()
	s.mu.Unlock()
	s.fireJob(j2, gen2)
	if _, ok := s.Get(j2.ID); ok {
		t.Fatal("debounced broken job must be removed")
	}
	if rec.count() != 1 {
		t.Fatal("debounced path must not enqueue")
	}
}

// --- patrolCheck ---

func TestSa104_Patrol_SkipsPausedZeroAndFreshJobs(t *testing.T) {
	s := NewScheduler(nil, "")
	defer s.Shutdown()

	paused := &Job{ID: "cron-1-p", CronExpr: "*/7 * * * *", Paused: true, NextFire: time.Now().Add(-time.Hour)}
	zero := &Job{ID: "cron-2-z", CronExpr: "*/7 * * * *", NextFire: time.Time{}}
	fresh := &Job{ID: "cron-3-f", CronExpr: "*/7 * * * *", NextFire: time.Now().Add(time.Hour)}
	sa104InsertJob(s, paused)
	sa104InsertJob(s, zero)
	sa104InsertJob(s, fresh)

	s.patrolCheck()

	for _, j := range []*Job{paused, zero, fresh} {
		got, ok := s.Get(j.ID)
		if !ok {
			t.Fatalf("%s must survive patrol", j.ID)
		}
		if !got.NextFire.Equal(j.NextFire) {
			t.Fatalf("%s must be untouched: %v want %v", j.ID, got.NextFire, j.NextFire)
		}
	}
}

func TestSa104_Patrol_MissedRecurringRescheduled(t *testing.T) {
	s := NewScheduler(nil, "")
	defer s.Shutdown()
	j := &Job{ID: "cron-1-m", CronExpr: "*/7 * * * *", Prompt: "p", Recurring: true, NextFire: time.Now().Add(-time.Hour)}
	sa104InsertJob(s, j)

	s.patrolCheck()

	got, ok := s.Get(j.ID)
	if !ok {
		t.Fatal("missed recurring job must be rescheduled, not removed")
	}
	if !got.NextFire.After(time.Now()) {
		t.Fatalf("rescheduled NextFire must be in the future, got %v", got.NextFire)
	}
}

func TestSa104_Patrol_MissedOneShotFiresOnce(t *testing.T) {
	rec := &sa104Rec{}
	s := NewScheduler(rec.fn(), "")
	defer s.Shutdown()
	j := &Job{ID: "cron-1-o", CronExpr: "*/7 * * * *", Prompt: "once", NextFire: time.Now().Add(-time.Hour)}
	sa104InsertJob(s, j)
	// Debounce guard keeps the AfterFunc(0) callback from racing our
	// assertions; the fire itself is covered by the fireJob matrix above.
	s.mu.Lock()
	s.lastEnqueue[j.ID] = time.Now()
	s.mu.Unlock()

	s.patrolCheck()

	got, ok := s.Get(j.ID)
	if !ok {
		t.Fatal("one-shot must remain (debounced)")
	}
	if got.NextFire.IsZero() {
		t.Fatal("missed one-shot must get a fire-now NextFire")
	}
}

func TestSa104_Patrol_RemovesBrokenExprJob(t *testing.T) {
	dir := t.TempDir()
	s := NewScheduler(nil, filepath.Join(dir, "cron.json"))
	defer s.Shutdown()
	j := &Job{ID: "cron-1-b", CronExpr: "junk expression", Recurring: true, NextFire: time.Now().Add(-time.Hour)}
	sa104InsertJob(s, j)

	s.patrolCheck()

	if _, ok := s.Get(j.ID); ok {
		t.Fatal("patrol must remove broken-expression jobs")
	}
}

func TestSa104_StartPatrol_Idempotent(t *testing.T) {
	s := NewScheduler(nil, "")
	defer s.Shutdown()
	s.startPatrol() // second call must early-return without spawning another goroutine
	s.startPatrol()
}

// --- migration extra branches ---

func TestSa104_Migrate_GuardClauses(t *testing.T) {
	dir := t.TempDir()
	// Empty args: no-op, no panic.
	MigrateWorkspaceJobs("", "", "")
	MigrateWorkspaceJobs(filepath.Join(dir, "a.json"), "", "ws")
	MigrateWorkspaceJobs("", filepath.Join(dir, "b.json"), "ws")

	// Corrupted old store: skip.
	corrupt := filepath.Join(dir, "corrupt.json")
	if err := os.WriteFile(corrupt, []byte("{bad"), 0644); err != nil {
		t.Fatal(err)
	}
	newPath := filepath.Join(dir, "new.json")
	MigrateWorkspaceJobs(corrupt, newPath, "ws")
	if _, err := os.Stat(newPath); !os.IsNotExist(err) {
		t.Fatal("corrupted old store must not produce a new store")
	}

	// Valid JSON but no bucket for this workspace: skip.
	other := oldStoreFile{workspaceKey("/somewhere/else"): workspaceBucket{Workspace: "/somewhere/else"}}
	raw, _ := json.Marshal(other)
	nobucket := filepath.Join(dir, "nobucket.json")
	if err := os.WriteFile(nobucket, raw, 0644); err != nil {
		t.Fatal(err)
	}
	MigrateWorkspaceJobs(nobucket, newPath, "ws")
	if _, err := os.Stat(newPath); !os.IsNotExist(err) {
		t.Fatal("missing bucket must not produce a new store")
	}
}

func TestSa104_Migrate_LockHeldSkips(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "cron-jobs.json")
	newPath := filepath.Join(dir, "new.json")
	ws := filepath.Join(dir, "ws")
	sa104WriteOldBucket(t, oldPath, ws)

	lockPath := oldPath + ".migrate.lock"
	release, ok := acquireMigrationLock(lockPath)
	if !ok {
		t.Skip("migration lock unavailable on this platform")
	}
	defer release()

	MigrateWorkspaceJobs(oldPath, newPath, ws)
	if _, err := os.Stat(newPath); !os.IsNotExist(err) {
		t.Fatal("migration must be skipped while another instance holds the lock")
	}
}

func TestSa104_Migrate_TombstoneTargetExistsSkips(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "cron-jobs.json")
	newPath := filepath.Join(dir, "new.json")
	ws := filepath.Join(dir, "ws")

	// Old store with a tombstone pointing at the new store, which exists.
	bucket := workspaceBucket{
		Workspace:  ws,
		MigratedTo: newPath,
		Jobs:       []jobJSON{{ID: "cron-1-t", CronExpr: "*/7 * * * *", Prompt: "p", Recurring: true}},
	}
	raw, _ := json.Marshal(oldStoreFile{workspaceKey(ws): bucket})
	if err := os.WriteFile(oldPath, raw, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte(`{"jobs":[]}`), 0644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(newPath)

	MigrateWorkspaceJobs(oldPath, newPath, ws) // bucket found, but its tombstone target exists → skip

	after, _ := os.ReadFile(newPath)
	if string(before) != string(after) {
		t.Fatal("tombstoned migration must not touch the existing target store")
	}
}

func sa104WriteOldBucket(t *testing.T, path, ws string) {
	t.Helper()
	bucket := workspaceBucket{
		Workspace: ws,
		Jobs:      []jobJSON{{ID: "cron-1-x", CronExpr: "*/7 * * * *", Prompt: "p", Recurring: true}},
	}
	raw, _ := json.Marshal(oldStoreFile{workspaceKey(ws): bucket})
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestSa104_Migrate_OneShotOnlyBucketDropsAll(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "cron-jobs.json")
	newPath := filepath.Join(dir, "new.json")
	ws := filepath.Join(dir, "ws")

	bucket := workspaceBucket{
		Workspace: ws,
		Jobs:      []jobJSON{{ID: "cron-1-o", CronExpr: "*/7 * * * *", Prompt: "once", Recurring: false}},
	}
	raw, _ := json.Marshal(oldStoreFile{workspaceKey(ws): bucket})
	if err := os.WriteFile(oldPath, raw, 0644); err != nil {
		t.Fatal(err)
	}

	MigrateWorkspaceJobs(oldPath, newPath, ws)

	if _, err := os.Stat(newPath); !os.IsNotExist(err) {
		t.Fatal("one-shot-only bucket must not create a new store")
	}
	// The bucket was the only one in the old store, so the emptied store
	// file is removed (migration.go: len(sf)==0 → os.Remove).
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		var after oldStoreFile
		rawAfter, _ := os.ReadFile(oldPath)
		if err := json.Unmarshal(rawAfter, &after); err == nil {
			if _, exists := after[workspaceKey(ws)]; exists {
				t.Fatal("consumed bucket (even all-one-shot) must be removed")
			}
		}
	}
}

// --- parser: parseFieldPart exhaustive branches (direct, table-driven) ---

func TestSa104_ParseFieldPart_Table(t *testing.T) {
	cases := []struct {
		part    string
		min     int
		max     int
		wantErr bool
		want    []int // expected values when wantErr is false
	}{
		// step path errors
		{part: "*/0", min: 0, max: 59, wantErr: true},
		{part: "*/x", min: 0, max: 59, wantErr: true},
		{part: "x-5/2", min: 0, max: 59, wantErr: true},
		{part: "5-x/2", min: 0, max: 59, wantErr: true},
		{part: "5-3/2", min: 0, max: 59, wantErr: true}, // #451 reversed range in step path
		{part: "60/5", min: 0, max: 59, wantErr: true},  // #415 out-of-range start
		{part: "10-70/2", min: 0, max: 59, wantErr: true},
		// plain range errors
		{part: "x-5", min: 0, max: 59, wantErr: true},
		{part: "5-x", min: 0, max: 59, wantErr: true},
		{part: "70-80", min: 0, max: 59, wantErr: true},
		{part: "50-10", min: 0, max: 59, wantErr: true}, // #439 reversed plain range
		// single value errors
		{part: "abc", min: 0, max: 59, wantErr: true},
		{part: "70", min: 0, max: 59, wantErr: true},
		// successes
		{part: "*/15", min: 0, max: 59, want: []int{0, 15, 30, 45}},
		{part: "10-20/5", min: 0, max: 59, want: []int{10, 15, 20}},
		{part: "5", min: 0, max: 59, want: []int{5}},
		{part: "*", min: 0, max: 3, want: []int{0, 1, 2, 3}},
		{part: "10-12", min: 0, max: 59, want: []int{10, 11, 12}},
	}
	for _, tc := range cases {
		vals := make(map[int]bool)
		err := parseFieldPart(tc.part, tc.min, tc.max, vals)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseFieldPart(%q,%d,%d): expected error, got values %v", tc.part, tc.min, tc.max, vals)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseFieldPart(%q,%d,%d): unexpected error %v", tc.part, tc.min, tc.max, err)
			continue
		}
		if len(vals) != len(tc.want) {
			t.Errorf("parseFieldPart(%q): got %v want %v", tc.part, vals, tc.want)
			continue
		}
		for _, v := range tc.want {
			if !vals[v] {
				t.Errorf("parseFieldPart(%q): missing value %d in %v", tc.part, v, vals)
			}
		}
	}
}

func TestSa104_NextTime_FieldCountAndWhitespace(t *testing.T) {
	base := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	if _, err := NextTime("*/7 * *", base); err == nil {
		t.Fatal("too few fields must error")
	}
	if _, err := NextTime("*/7 * * * * * *", base); err == nil {
		t.Fatal("too many fields must error")
	}
	if _, err := NextTime("", base); err == nil {
		t.Fatal("empty expression must error")
	}
	got, err := NextTime("  */7   *   *   *   *  ", base)
	if err != nil {
		t.Fatalf("extra whitespace must be tolerated: %v", err)
	}
	want := time.Date(2026, 6, 15, 10, 7, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
