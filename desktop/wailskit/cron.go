package wailskit

import (
	"context"
	"fmt"
	"time"

	"github.com/topcheer/ggcode/internal/cron"
	"github.com/topcheer/ggcode/internal/provider"
)

// CronJobInfo is the JSON-serializable representation of a cron job for the desktop frontend.
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

// cronJobToInfo converts a cron.Job to the frontend-friendly CronJobInfo.
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

// ListCronJobs returns all cron jobs for the current session.
func (b *ChatBridge) ListCronJobs() []CronJobInfo {
	if b.cronScheduler == nil {
		return nil
	}
	jobs := b.cronScheduler.List()
	result := make([]CronJobInfo, len(jobs))
	for i, j := range jobs {
		result[i] = cronJobToInfo(j)
	}
	return result
}

// GetCronJob returns a single cron job by ID.
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

// CreateCronJob creates a new cron job.
func (b *ChatBridge) CreateCronJob(cronExpr, prompt string, recurring, queueIfBusy bool) (CronJobInfo, error) {
	if b.cronScheduler == nil {
		return CronJobInfo{}, fmt.Errorf("cron scheduler not available")
	}
	job, err := b.cronScheduler.Create(cronExpr, prompt, recurring, queueIfBusy)
	if err != nil {
		return CronJobInfo{}, err
	}
	return cronJobToInfo(job), nil
}

// UpdateCronJob updates an existing cron job's cron expression, prompt, and queueIfBusy.
func (b *ChatBridge) UpdateCronJob(id, cronExpr, prompt string, queueIfBusy bool) (CronJobInfo, error) {
	if b.cronScheduler == nil {
		return CronJobInfo{}, fmt.Errorf("cron scheduler not available")
	}
	job, err := b.cronScheduler.Update(id, &cronExpr, &prompt, &queueIfBusy)
	if err != nil {
		return CronJobInfo{}, err
	}
	return cronJobToInfo(job), nil
}

// DeleteCronJob removes a cron job by ID.
func (b *ChatBridge) DeleteCronJob(id string) error {
	if b.cronScheduler == nil {
		return fmt.Errorf("cron scheduler not available")
	}
	_, err := b.cronScheduler.DeleteWithError(id)
	return err
}

// PauseCronJob suspends a cron job.
func (b *ChatBridge) PauseCronJob(id string) error {
	if b.cronScheduler == nil {
		return fmt.Errorf("cron scheduler not available")
	}
	return b.cronScheduler.Pause(id)
}

// ResumeCronJob reactivates a paused cron job.
func (b *ChatBridge) ResumeCronJob(id string) error {
	if b.cronScheduler == nil {
		return fmt.Errorf("cron scheduler not available")
	}
	return b.cronScheduler.Resume(id)
}

// GenerateCronPrompt uses the current LLM provider to generate a concise, actionable
// cron prompt from a natural-language description. This is a synchronous single-shot
// Chat call (no agent loop, no tools).
func (b *ChatBridge) GenerateCronPrompt(description string) (string, error) {
	if b.agent == nil {
		return "", fmt.Errorf("agent not initialized")
	}
	prov := b.agent.Provider()
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

	// Extract text from the response message content blocks.
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
