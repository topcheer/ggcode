package wailskit

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/cron"
	"github.com/topcheer/ggcode/internal/provider"
)

type CronJobInfo struct {
	ID          string `json:"id"`
	CronExpr    string `json:"cronExpr"`
	Prompt      string `json:"prompt"`
	Recurring   bool   `json:"recurring"`
	QueueIfBusy bool   `json:"queueIfBusy"`
	Paused      bool   `json:"paused"`
	CreatedAt   string `json:"createdAt"`
	NextFire    string `json:"nextFire"`
}

func cronJobToInfo(j cron.Job) CronJobInfo {
	info := CronJobInfo{
		ID:          j.ID,
		CronExpr:    j.CronExpr,
		Prompt:      j.Prompt,
		Recurring:   j.Recurring,
		QueueIfBusy: j.QueueIfBusy,
		Paused:      j.Paused,
	}
	if !j.CreatedAt.IsZero() {
		info.CreatedAt = j.CreatedAt.Format(time.RFC3339)
	}
	if !j.NextFire.IsZero() {
		info.NextFire = j.NextFire.Format(time.RFC3339)
	}
	return info
}

func (b *ChatBridge) ListCronJobs() ([]CronJobInfo, error) {
	if b.cronScheduler == nil {
		return nil, fmt.Errorf("cron scheduler not available")
	}
	jobs := b.cronScheduler.List()
	result := make([]CronJobInfo, len(jobs))
	for i, j := range jobs {
		result[i] = cronJobToInfo(j)
	}
	return result, nil
}

func (b *ChatBridge) GetCronJob(id string) (CronJobInfo, error) {
	if b.cronScheduler == nil {
		return CronJobInfo{}, fmt.Errorf("cron scheduler not available")
	}
	j, ok := b.cronScheduler.Get(id)
	if !ok {
		return CronJobInfo{}, fmt.Errorf("cron job %q not found", id)
	}
	return cronJobToInfo(j), nil
}

func (b *ChatBridge) CreateCronJob(cronExpr, prompt string, recurring, queueIfBusy bool) (CronJobInfo, error) {
	if b.cronScheduler == nil {
		return CronJobInfo{}, fmt.Errorf("cron scheduler not available")
	}

	if strings.TrimSpace(prompt) == "" {
		return CronJobInfo{}, fmt.Errorf("prompt must not be empty")
	}
	job, err := b.cronScheduler.Create(cronExpr, prompt, recurring, queueIfBusy)
	if err != nil {
		return CronJobInfo{}, err
	}
	return cronJobToInfo(job), nil
}

func (b *ChatBridge) UpdateCronJob(id, cronExpr, prompt string, queueIfBusy bool) (CronJobInfo, error) {
	if b.cronScheduler == nil {
		return CronJobInfo{}, fmt.Errorf("cron scheduler not available")
	}
	if strings.TrimSpace(prompt) == "" {
		return CronJobInfo{}, fmt.Errorf("prompt cannot be empty (#617): an empty-prompt cron job is a dead job; submit the full prompt with the update")
	}
	var cronPtr *string
	if cronExpr != "" {
		cronPtr = &cronExpr
	}
	promptPtr := &prompt
	// #4977: the desktop editor is a full-state save channel - the frontend
	// (CronManagement.tsx:75) passes the checkbox's plain bool on every save.
	// The old `if queueIfBusy { qb = &queueIfBusy }` folded an explicit false
	// into nil ("keep original" in Scheduler.Update), so unchecking
	// queue-if-busy silently failed while the UI showed success. Pass the
	// pointer unconditionally; agents needing partial-update semantics use the
	// cron_update tool, whose *bool JSON binding already distinguishes absent
	// from explicit false.
	qb := &queueIfBusy
	job, err := b.cronScheduler.Update(id, cronPtr, promptPtr, qb)
	if err != nil {
		return CronJobInfo{}, err
	}
	return cronJobToInfo(job), nil
}

func (b *ChatBridge) DeleteCronJob(id string) error {
	if b.cronScheduler == nil {
		return fmt.Errorf("cron scheduler not available")
	}
	_, err := b.cronScheduler.DeleteWithError(id)
	return err
}

func (b *ChatBridge) PauseCronJob(id string) error {
	if b.cronScheduler == nil {
		return fmt.Errorf("cron scheduler not available")
	}
	return b.cronScheduler.Pause(id)
}

func (b *ChatBridge) ResumeCronJob(id string) error {
	if b.cronScheduler == nil {
		return fmt.Errorf("cron scheduler not available")
	}
	return b.cronScheduler.Resume(id)
}

func (b *ChatBridge) GenerateCronPrompt(description string) (string, error) {

	b.mu.Lock()
	agentSnapshot := b.agent
	b.mu.Unlock()
	if agentSnapshot == nil {
		return "", fmt.Errorf("agent not initialized")
	}
	prov := agentSnapshot.Provider()
	if prov == nil {
		return "", fmt.Errorf("provider not available")
	}

	systemPrompt := `You are a cron prompt generator for an AI coding agent.
Given a short description of a recurring task, generate a concise, actionable prompt
that the agent can execute when the cron job fires. The prompt should be self-contained
and specific enough to produce useful results on each run. Output ONLY the prompt text,
no explanations or markdown formatting.`

	messages := []provider.Message{
		{Role: "system", Content: []provider.ContentBlock{{Type: "text", Text: systemPrompt}}},
		{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: description}}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := prov.Chat(ctx, messages, nil)
	if err != nil {
		return "", fmt.Errorf("LLM call failed: %w", err)
	}

	var text string
	for _, block := range resp.Message.Content {
		if block.Type == "text" {
			text += block.Text
		}
	}
	if text == "" {
		return "", fmt.Errorf("LLM returned empty response")
	}
	return text, nil
}
