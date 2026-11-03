package swarm

import (
	"sync"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/task"
)

func TestTeammateSnapshot_ConcurrentWithSpawnTeammate(t *testing.T) {
	m, _ := testManager(t)

	teamSnap := m.CreateTeam("race-team", "")
	teamID := teamSnap.ID

	m.SetWorkingDir("/tmp")

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:

					m.TeammateSnapshot("tm-999")
				}
			}
		}()
	}

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			name := time.Now().Format("15:04:05.000")
			m.SpawnTeammate(teamID, name, "32", nil)
		}(i)
	}

	time.Sleep(500 * time.Millisecond)
	close(stop)
	wg.Wait()
}

func TestTeam_TeammatesMapConcurrentAccess(t *testing.T) {
	team := &Team{
		ID:        "team-1",
		Name:      "concurrent-test",
		Teammates: make(map[string]*Teammate),
		Tasks:     task.NewManager(),
		CreatedAt: time.Now(),
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					team.mu.RLock()
					for id := range team.Teammates {
						_ = id
					}
					team.mu.RUnlock()
				}
			}
		}()
	}

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id := "tm-concurrent-" + time.Now().Format("15:04:05.000")
			tm := &Teammate{
				ID:        id,
				Name:      "concurrent",
				Status:    TeammateIdle,
				Inbox:     make(chan MailMessage, 16),
				CreatedAt: time.Now(),
			}
			team.mu.Lock()
			team.Teammates[id] = tm
			team.mu.Unlock()

			time.Sleep(10 * time.Millisecond)

			team.mu.Lock()
			delete(team.Teammates, id)
			team.mu.Unlock()
		}(i)
	}

	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()
}
