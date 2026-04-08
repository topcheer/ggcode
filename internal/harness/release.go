package harness

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

type ReleasePlan struct {
	BatchID       string         `json:"batch_id"`
	Tasks         []*Task        `json:"tasks,omitempty"`
	Owners        map[string]int `json:"owners,omitempty"`
	Contexts      map[string]int `json:"contexts,omitempty"`
	GeneratedAt   time.Time      `json:"generated_at"`
	Environment   string         `json:"environment,omitempty"`
	OwnerFilter   string         `json:"owner_filter,omitempty"`
	ContextFilter string         `json:"context_filter,omitempty"`
	GroupBy       string         `json:"group_by,omitempty"`
	GroupLabel    string         `json:"group_label,omitempty"`
	RolloutID     string         `json:"rollout_id,omitempty"`
	WaveOrder     int            `json:"wave_order,omitempty"`
	WaveStatus    string         `json:"wave_status,omitempty"`
	GateStatus    string         `json:"gate_status,omitempty"`
	StatusNote    string         `json:"status_note,omitempty"`
	GateNote      string         `json:"gate_note,omitempty"`
	ActivatedAt   *time.Time     `json:"activated_at,omitempty"`
	GateCheckedAt *time.Time     `json:"gate_checked_at,omitempty"`
	PausedAt      *time.Time     `json:"paused_at,omitempty"`
	AbortedAt     *time.Time     `json:"aborted_at,omitempty"`
	CompletedAt   *time.Time     `json:"completed_at,omitempty"`
	ReportPath    string         `json:"report_path,omitempty"`
}

type ReleaseWavePlan struct {
	RolloutID     string         `json:"rollout_id,omitempty"`
	GroupBy       string         `json:"group_by"`
	Groups        []*ReleasePlan `json:"groups,omitempty"`
	TotalTasks    int            `json:"total_tasks"`
	GeneratedAt   time.Time      `json:"generated_at"`
	Environment   string         `json:"environment,omitempty"`
	OwnerFilter   string         `json:"owner_filter,omitempty"`
	ContextFilter string         `json:"context_filter,omitempty"`
}

func BuildReleasePlan(project Project, cfg *Config) (*ReleasePlan, error) {
	return BuildReleasePlanWithOptions(project, cfg, ReleasePlanOptions{})
}

type ReleasePlanOptions struct {
	Owner       string
	Context     string
	Environment string
}

const (
	ReleaseGroupByOwner   = "owner"
	ReleaseGroupByContext = "context"
	ReleaseWavePlanned    = "planned"
	ReleaseWaveActive     = "active"
	ReleaseWavePaused     = "paused"
	ReleaseWaveAborted    = "aborted"
	ReleaseWaveCompleted  = "completed"
	ReleaseGatePending    = "pending"
	ReleaseGateApproved   = "approved"
	ReleaseGateRejected   = "rejected"
)

func BuildReleasePlanWithOptions(project Project, cfg *Config, opts ReleasePlanOptions) (*ReleasePlan, error) {
	tasks, err := ListTasks(project)
	if err != nil {
		return nil, err
	}
	ownerFilter := strings.TrimSpace(opts.Owner)
	contextFilter := strings.TrimSpace(opts.Context)
	environment := strings.TrimSpace(opts.Environment)
	contextCfg, err := ResolveContext(cfg, contextFilter)
	if err != nil {
		return nil, err
	}
	plan := &ReleasePlan{
		BatchID:       generateReleaseBatchID(),
		Owners:        make(map[string]int),
		Contexts:      make(map[string]int),
		GeneratedAt:   time.Now().UTC(),
		Environment:   environment,
		OwnerFilter:   ownerFilter,
		ContextFilter: normalizedReleaseContextLabel(contextCfg, contextFilter),
	}
	for _, task := range tasks {
		if !taskReleaseReady(task) {
			continue
		}
		if ownerFilter != "" && !ownerMatches(cfg, task, ownerFilter) {
			continue
		}
		if !releaseContextMatches(cfg, task, contextCfg, contextFilter) {
			continue
		}
		plan.Tasks = append(plan.Tasks, task)
		plan.Owners[ownerForTask(cfg, task)]++
		plan.Contexts[firstNonEmptyText(task.ContextPath, task.ContextName, "unscoped")]++
	}
	sort.Slice(plan.Tasks, func(i, j int) bool {
		return plan.Tasks[i].UpdatedAt.After(plan.Tasks[j].UpdatedAt)
	})
	return plan, nil
}

func BuildReleaseWavePlan(project Project, cfg *Config, opts ReleasePlanOptions, groupBy string) (*ReleaseWavePlan, error) {
	groupBy, err := normalizeReleaseGroupBy(groupBy)
	if err != nil {
		return nil, err
	}
	basePlan, err := BuildReleasePlanWithOptions(project, cfg, opts)
	if err != nil {
		return nil, err
	}
	waves := &ReleaseWavePlan{
		RolloutID:     generateReleaseRolloutID(),
		GroupBy:       groupBy,
		GeneratedAt:   basePlan.GeneratedAt,
		Environment:   basePlan.Environment,
		OwnerFilter:   basePlan.OwnerFilter,
		ContextFilter: basePlan.ContextFilter,
	}
	if len(basePlan.Tasks) == 0 {
		return waves, nil
	}
	grouped := make(map[string]*ReleasePlan)
	for _, task := range basePlan.Tasks {
		if task == nil {
			continue
		}
		label := releaseGroupLabel(cfg, task, groupBy)
		plan := grouped[label]
		if plan == nil {
			plan = &ReleasePlan{
				BatchID:       generateReleaseBatchID(),
				Owners:        make(map[string]int),
				Contexts:      make(map[string]int),
				GeneratedAt:   basePlan.GeneratedAt,
				Environment:   basePlan.Environment,
				OwnerFilter:   basePlan.OwnerFilter,
				ContextFilter: basePlan.ContextFilter,
				GroupBy:       groupBy,
				GroupLabel:    label,
				RolloutID:     waves.RolloutID,
			}
			grouped[label] = plan
			waves.Groups = append(waves.Groups, plan)
		}
		plan.Tasks = append(plan.Tasks, task)
		plan.Owners[ownerForTask(cfg, task)]++
		plan.Contexts[firstNonEmptyText(task.ContextPath, task.ContextName, "unscoped")]++
		waves.TotalTasks++
	}
	sort.Slice(waves.Groups, func(i, j int) bool {
		return waves.Groups[i].GroupLabel < waves.Groups[j].GroupLabel
	})
	for i, plan := range waves.Groups {
		plan.WaveOrder = i + 1
		sort.Slice(plan.Tasks, func(i, j int) bool {
			return plan.Tasks[i].UpdatedAt.After(plan.Tasks[j].UpdatedAt)
		})
	}
	return waves, nil
}

func ApplyReleasePlan(project Project, plan *ReleasePlan, note string) (*ReleasePlan, error) {
	if plan == nil {
		return nil, fmt.Errorf("missing release plan")
	}
	if len(plan.Tasks) == 0 {
		return nil, fmt.Errorf("no harness tasks are ready for release")
	}
	for i, task := range plan.Tasks {
		if task == nil {
			continue
		}
		loaded, err := LoadTask(project, task.ID)
		if err != nil {
			return nil, err
		}
		if !taskReleaseReady(loaded) {
			return nil, fmt.Errorf("task %s is no longer release-ready", loaded.ID)
		}
		now := time.Now().UTC()
		loaded.ReleaseBatchID = plan.BatchID
		loaded.ReleaseNotes = strings.TrimSpace(note)
		loaded.ReleasedAt = &now
		if err := SaveTask(project, loaded); err != nil {
			return nil, err
		}
		plan.Tasks[i] = loaded
	}
	reportPath, err := writeReleasePlan(project, plan)
	if err != nil {
		return nil, err
	}
	plan.ReportPath = reportPath
	return plan, nil
}

func ApplyReleaseWavePlan(project Project, waves *ReleaseWavePlan, note, batchBase string) (*ReleaseWavePlan, error) {
	if waves == nil {
		return nil, fmt.Errorf("missing release waves")
	}
	if len(waves.Groups) == 0 {
		return nil, fmt.Errorf("no harness tasks are ready for release")
	}
	for i, group := range waves.Groups {
		if group == nil {
			continue
		}
		group.RolloutID = firstNonEmptyText(strings.TrimSpace(batchBase), waves.RolloutID)
		group.WaveOrder = i + 1
		group.WaveStatus = ReleaseWavePlanned
		group.GateStatus = ReleaseGatePending
		group.StatusNote = ""
		group.GateNote = ""
		group.ActivatedAt = nil
		group.GateCheckedAt = nil
		group.PausedAt = nil
		group.AbortedAt = nil
		group.CompletedAt = nil
		if i == 0 {
			now := time.Now().UTC()
			group.WaveStatus = ReleaseWaveActive
			group.GateStatus = ReleaseGateApproved
			group.ActivatedAt = &now
			group.GateCheckedAt = &now
		}
		if strings.TrimSpace(batchBase) != "" {
			group.BatchID = deriveReleaseWaveBatchID(batchBase, waves, group)
		}
		applied, err := ApplyReleasePlan(project, group, note)
		if err != nil {
			return nil, err
		}
		waves.Groups[i] = applied
	}
	return waves, nil
}

func FormatReleasePlan(plan *ReleasePlan) string {
	if plan == nil || len(plan.Tasks) == 0 {
		return "No harness tasks are ready for release."
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Harness release plan %s\n", plan.BatchID))
	if plan.Environment != "" {
		b.WriteString(fmt.Sprintf("- environment: %s\n", plan.Environment))
	}
	if plan.OwnerFilter != "" || plan.ContextFilter != "" {
		b.WriteString("- scope:")
		if plan.OwnerFilter != "" {
			b.WriteString(fmt.Sprintf(" owner=%s", plan.OwnerFilter))
		}
		if plan.ContextFilter != "" {
			b.WriteString(fmt.Sprintf(" context=%s", plan.ContextFilter))
		}
		b.WriteString("\n")
	}
	b.WriteString(fmt.Sprintf("- tasks: %d\n", len(plan.Tasks)))
	if len(plan.Owners) > 0 {
		b.WriteString("- owners:\n")
		for _, owner := range sortedMapKeys(plan.Owners) {
			b.WriteString(fmt.Sprintf("  - %s: %d\n", owner, plan.Owners[owner]))
		}
	}
	if len(plan.Contexts) > 0 {
		b.WriteString("- contexts:\n")
		for _, context := range sortedMapKeys(plan.Contexts) {
			b.WriteString(fmt.Sprintf("  - %s: %d\n", context, plan.Contexts[context]))
		}
	}
	for _, task := range plan.Tasks {
		if task == nil {
			continue
		}
		b.WriteString(fmt.Sprintf("- %s %s\n", task.ID, task.Goal))
	}
	if plan.ReportPath != "" {
		b.WriteString(fmt.Sprintf("Report: %s\n", plan.ReportPath))
	}
	return strings.TrimRight(b.String(), "\n")
}

func FormatReleaseWavePlan(waves *ReleaseWavePlan) string {
	if waves == nil || len(waves.Groups) == 0 {
		return "No harness tasks are ready for release."
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Harness release waves by %s\n", waves.GroupBy))
	if waves.RolloutID != "" {
		b.WriteString(fmt.Sprintf("- rollout: %s\n", waves.RolloutID))
	}
	if waves.Environment != "" {
		b.WriteString(fmt.Sprintf("- environment: %s\n", waves.Environment))
	}
	if waves.OwnerFilter != "" || waves.ContextFilter != "" {
		b.WriteString("- scope:")
		if waves.OwnerFilter != "" {
			b.WriteString(fmt.Sprintf(" owner=%s", waves.OwnerFilter))
		}
		if waves.ContextFilter != "" {
			b.WriteString(fmt.Sprintf(" context=%s", waves.ContextFilter))
		}
		b.WriteString("\n")
	}
	b.WriteString(fmt.Sprintf("- groups: %d\n", len(waves.Groups)))
	b.WriteString(fmt.Sprintf("- tasks: %d\n", waves.TotalTasks))
	for _, group := range waves.Groups {
		if group == nil {
			continue
		}
		b.WriteString(fmt.Sprintf("- %s %s order=%d status=%s gate=%s batch=%s tasks=%d\n", waves.GroupBy, group.GroupLabel, group.WaveOrder, releaseWaveStatus(group), releaseWaveGateStatus(group), group.BatchID, len(group.Tasks)))
		if group.GateNote != "" {
			b.WriteString(fmt.Sprintf("  gate_note: %s\n", group.GateNote))
		}
		if group.StatusNote != "" {
			b.WriteString(fmt.Sprintf("  note: %s\n", group.StatusNote))
		}
		for _, task := range group.Tasks {
			if task == nil {
				continue
			}
			b.WriteString(fmt.Sprintf("  - %s %s\n", task.ID, task.Goal))
		}
		if group.ReportPath != "" {
			b.WriteString(fmt.Sprintf("  report: %s\n", group.ReportPath))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func taskReleaseReady(task *Task) bool {
	return task != nil && task.PromotionStatus == PromotionApplied && task.ReleasedAt == nil
}

func normalizeReleaseGroupBy(groupBy string) (string, error) {
	groupBy = strings.ToLower(strings.TrimSpace(groupBy))
	switch groupBy {
	case ReleaseGroupByOwner, ReleaseGroupByContext:
		return groupBy, nil
	case "":
		return "", fmt.Errorf("missing release grouping mode")
	default:
		return "", fmt.Errorf("unsupported release grouping %q", groupBy)
	}
}

func releaseGroupLabel(cfg *Config, task *Task, groupBy string) string {
	switch groupBy {
	case ReleaseGroupByOwner:
		return ownerForTask(cfg, task)
	case ReleaseGroupByContext:
		return firstNonEmptyText(task.ContextPath, task.ContextName, "unscoped")
	default:
		return "default"
	}
}

func releaseContextMatches(cfg *Config, task *Task, contextCfg *ContextConfig, raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return true
	}
	if task == nil {
		return false
	}
	if contextCfg != nil {
		return strings.EqualFold(strings.TrimSpace(task.ContextName), strings.TrimSpace(contextCfg.Name)) ||
			filepath.Clean(task.ContextPath) == filepath.Clean(contextCfg.Path)
	}
	return strings.EqualFold(strings.TrimSpace(task.ContextName), raw) ||
		filepath.Clean(task.ContextPath) == filepath.Clean(raw)
}

func normalizedReleaseContextLabel(contextCfg *ContextConfig, raw string) string {
	if contextCfg == nil {
		return strings.TrimSpace(raw)
	}
	return firstNonEmptyText(contextCfg.Path, contextCfg.Name)
}

func deriveReleaseWaveBatchID(base string, waves *ReleaseWavePlan, group *ReleasePlan) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return group.BatchID
	}
	if waves != nil && len(waves.Groups) == 1 {
		return base
	}
	suffix := sanitizeReleaseBatchSuffix(firstNonEmptyText(group.GroupBy, "group") + "-" + firstNonEmptyText(group.GroupLabel, "default"))
	if suffix == "" {
		return base
	}
	return base + "-" + suffix
}

func ListReleaseWaveRollouts(project Project) ([]*ReleaseWavePlan, error) {
	plans, err := loadReleasePlans(project)
	if err != nil {
		return nil, err
	}
	grouped := make(map[string]*ReleaseWavePlan)
	for _, plan := range plans {
		if plan == nil || strings.TrimSpace(plan.RolloutID) == "" || strings.TrimSpace(plan.GroupBy) == "" {
			continue
		}
		rollout := grouped[plan.RolloutID]
		if rollout == nil {
			rollout = &ReleaseWavePlan{
				RolloutID:     plan.RolloutID,
				GroupBy:       plan.GroupBy,
				GeneratedAt:   plan.GeneratedAt,
				Environment:   plan.Environment,
				OwnerFilter:   plan.OwnerFilter,
				ContextFilter: plan.ContextFilter,
			}
			grouped[plan.RolloutID] = rollout
		}
		rollout.Groups = append(rollout.Groups, plan)
		rollout.TotalTasks += len(plan.Tasks)
	}
	rollouts := make([]*ReleaseWavePlan, 0, len(grouped))
	for _, rollout := range grouped {
		sort.Slice(rollout.Groups, func(i, j int) bool {
			left := rollout.Groups[i]
			right := rollout.Groups[j]
			if left.WaveOrder != right.WaveOrder {
				return left.WaveOrder < right.WaveOrder
			}
			return left.GroupLabel < right.GroupLabel
		})
		rollouts = append(rollouts, rollout)
	}
	sort.Slice(rollouts, func(i, j int) bool {
		return rollouts[i].GeneratedAt.After(rollouts[j].GeneratedAt)
	})
	return rollouts, nil
}

func FilterReleaseWaveRolloutsByEnvironment(rollouts []*ReleaseWavePlan, environment string) []*ReleaseWavePlan {
	environment = strings.TrimSpace(environment)
	if environment == "" {
		return rollouts
	}
	var filtered []*ReleaseWavePlan
	for _, rollout := range rollouts {
		if rollout != nil && strings.EqualFold(strings.TrimSpace(rollout.Environment), environment) {
			filtered = append(filtered, rollout)
		}
	}
	return filtered
}

func AdvanceReleaseWaveRollout(project Project, rolloutID string) (*ReleaseWavePlan, error) {
	rolloutID = strings.TrimSpace(rolloutID)
	if rolloutID == "" {
		return nil, fmt.Errorf("missing rollout ID")
	}
	rollouts, err := ListReleaseWaveRollouts(project)
	if err != nil {
		return nil, err
	}
	var target *ReleaseWavePlan
	for _, rollout := range rollouts {
		if rollout != nil && rollout.RolloutID == rolloutID {
			target = rollout
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("unknown release rollout %q", rolloutID)
	}
	activeIndex := -1
	nextPlanned := -1
	for i, group := range target.Groups {
		if group == nil {
			continue
		}
		switch releaseWaveStatus(group) {
		case ReleaseWavePaused:
			return nil, fmt.Errorf("release rollout %s is paused; resume it before advancing", rolloutID)
		case ReleaseWaveAborted:
			return nil, fmt.Errorf("release rollout %s has been aborted", rolloutID)
		}
		switch group.WaveStatus {
		case ReleaseWaveActive:
			activeIndex = i
		case ReleaseWavePlanned:
			if nextPlanned == -1 {
				nextPlanned = i
			}
		}
	}
	now := time.Now().UTC()
	if nextPlanned >= 0 && releaseWaveGateStatus(target.Groups[nextPlanned]) != ReleaseGateApproved {
		return nil, fmt.Errorf("release rollout %s wave %d is not approved; approve it before advancing", rolloutID, target.Groups[nextPlanned].WaveOrder)
	}
	if activeIndex >= 0 {
		target.Groups[activeIndex].WaveStatus = ReleaseWaveCompleted
		target.Groups[activeIndex].CompletedAt = &now
		if _, err := persistReleasePlan(project, target.Groups[activeIndex]); err != nil {
			return nil, err
		}
	}
	if nextPlanned >= 0 {
		target.Groups[nextPlanned].WaveStatus = ReleaseWaveActive
		if target.Groups[nextPlanned].ActivatedAt == nil {
			target.Groups[nextPlanned].ActivatedAt = &now
		}
		if _, err := persistReleasePlan(project, target.Groups[nextPlanned]); err != nil {
			return nil, err
		}
		return target, nil
	}
	if activeIndex >= 0 {
		return target, nil
	}
	return nil, fmt.Errorf("release rollout %s has no waves left to advance", rolloutID)
}

func PauseReleaseWaveRollout(project Project, rolloutID, note string) (*ReleaseWavePlan, error) {
	target, err := loadReleaseRollout(project, rolloutID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	for _, group := range target.Groups {
		if group != nil && releaseWaveStatus(group) == ReleaseWaveActive {
			group.WaveStatus = ReleaseWavePaused
			group.StatusNote = strings.TrimSpace(note)
			group.PausedAt = &now
			if _, err := persistReleasePlan(project, group); err != nil {
				return nil, err
			}
			return target, nil
		}
	}
	return nil, fmt.Errorf("release rollout %s has no active wave to pause", rolloutID)
}

func ResumeReleaseWaveRollout(project Project, rolloutID, note string) (*ReleaseWavePlan, error) {
	target, err := loadReleaseRollout(project, rolloutID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	for _, group := range target.Groups {
		if group != nil && releaseWaveStatus(group) == ReleaseWavePaused {
			if releaseWaveGateStatus(group) == ReleaseGateRejected {
				return nil, fmt.Errorf("release rollout %s wave %d is blocked by a rejected gate", rolloutID, group.WaveOrder)
			}
			group.WaveStatus = ReleaseWaveActive
			group.StatusNote = strings.TrimSpace(note)
			group.PausedAt = nil
			if group.ActivatedAt == nil {
				group.ActivatedAt = &now
			}
			if _, err := persistReleasePlan(project, group); err != nil {
				return nil, err
			}
			return target, nil
		}
	}
	return nil, fmt.Errorf("release rollout %s has no paused wave to resume", rolloutID)
}

func AbortReleaseWaveRollout(project Project, rolloutID, note string) (*ReleaseWavePlan, error) {
	target, err := loadReleaseRollout(project, rolloutID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	changed := false
	for _, group := range target.Groups {
		if group == nil {
			continue
		}
		switch releaseWaveStatus(group) {
		case ReleaseWaveCompleted, ReleaseWaveAborted:
			continue
		default:
			group.WaveStatus = ReleaseWaveAborted
			group.GateStatus = firstNonEmptyText(strings.TrimSpace(group.GateStatus), ReleaseGateRejected)
			group.StatusNote = strings.TrimSpace(note)
			group.AbortedAt = &now
			group.PausedAt = nil
			if _, err := persistReleasePlan(project, group); err != nil {
				return nil, err
			}
			changed = true
		}
	}
	if !changed {
		return nil, fmt.Errorf("release rollout %s has no remaining waves to abort", rolloutID)
	}
	return target, nil
}

func ApproveReleaseWaveGate(project Project, rolloutID string, waveOrder int, note string) (*ReleaseWavePlan, error) {
	target, index, err := loadGateTarget(project, rolloutID, waveOrder)
	if err != nil {
		return nil, err
	}
	group := target.Groups[index]
	now := time.Now().UTC()
	group.GateStatus = ReleaseGateApproved
	group.GateNote = strings.TrimSpace(note)
	group.GateCheckedAt = &now
	if _, err := persistReleasePlan(project, group); err != nil {
		return nil, err
	}
	return target, nil
}

func RejectReleaseWaveGate(project Project, rolloutID string, waveOrder int, note string) (*ReleaseWavePlan, error) {
	target, index, err := loadGateTarget(project, rolloutID, waveOrder)
	if err != nil {
		return nil, err
	}
	group := target.Groups[index]
	if releaseWaveStatus(group) != ReleaseWavePlanned {
		return nil, fmt.Errorf("release rollout %s wave %d is not planned", rolloutID, group.WaveOrder)
	}
	now := time.Now().UTC()
	group.GateStatus = ReleaseGateRejected
	group.GateNote = strings.TrimSpace(note)
	group.GateCheckedAt = &now
	if _, err := persistReleasePlan(project, group); err != nil {
		return nil, err
	}
	return target, nil
}

func FormatReleaseWaveRollouts(rollouts []*ReleaseWavePlan) string {
	if len(rollouts) == 0 {
		return "No harness release rollouts recorded."
	}
	var b strings.Builder
	b.WriteString("Harness release rollouts\n")
	for _, rollout := range rollouts {
		if rollout == nil {
			continue
		}
		b.WriteString(fmt.Sprintf("- rollout=%s group_by=%s environment=%s waves=%d tasks=%d\n", rollout.RolloutID, rollout.GroupBy, firstNonEmptyText(rollout.Environment, "default"), len(rollout.Groups), rollout.TotalTasks))
		for _, group := range rollout.Groups {
			if group == nil {
				continue
			}
			b.WriteString(fmt.Sprintf("  - order=%d status=%s gate=%s label=%s batch=%s\n", group.WaveOrder, releaseWaveStatus(group), releaseWaveGateStatus(group), group.GroupLabel, group.BatchID))
			if group.GateNote != "" {
				b.WriteString(fmt.Sprintf("    gate_note=%s\n", group.GateNote))
			}
			if group.StatusNote != "" {
				b.WriteString(fmt.Sprintf("    note=%s\n", group.StatusNote))
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func writeReleasePlan(project Project, plan *ReleasePlan) (string, error) {
	return persistReleasePlan(project, plan)
}

func generateReleaseBatchID() string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "release-" + time.Now().UTC().Format("20060102-150405")
	}
	return "release-" + time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(buf[:])
}

func generateReleaseRolloutID() string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "rollout-" + time.Now().UTC().Format("20060102-150405")
	}
	return "rollout-" + time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(buf[:])
}

func sortedMapKeys(values map[string]int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sanitizeReleaseBatchSuffix(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		case lastDash:
			continue
		default:
			b.WriteRune('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func releaseWaveStatus(plan *ReleasePlan) string {
	if plan == nil {
		return ReleaseWavePlanned
	}
	return firstNonEmptyText(strings.TrimSpace(plan.WaveStatus), ReleaseWavePlanned)
}

func releaseWaveGateStatus(plan *ReleasePlan) string {
	if plan == nil {
		return ReleaseGatePending
	}
	if gate := strings.TrimSpace(plan.GateStatus); gate != "" {
		return gate
	}
	switch releaseWaveStatus(plan) {
	case ReleaseWaveActive, ReleaseWavePaused, ReleaseWaveCompleted:
		return ReleaseGateApproved
	default:
		return ReleaseGatePending
	}
}

func loadReleaseRollout(project Project, rolloutID string) (*ReleaseWavePlan, error) {
	rolloutID = strings.TrimSpace(rolloutID)
	if rolloutID == "" {
		return nil, fmt.Errorf("missing rollout ID")
	}
	rollouts, err := ListReleaseWaveRollouts(project)
	if err != nil {
		return nil, err
	}
	for _, rollout := range rollouts {
		if rollout != nil && rollout.RolloutID == rolloutID {
			return rollout, nil
		}
	}
	return nil, fmt.Errorf("unknown release rollout %q", rolloutID)
}

func loadGateTarget(project Project, rolloutID string, waveOrder int) (*ReleaseWavePlan, int, error) {
	target, err := loadReleaseRollout(project, rolloutID)
	if err != nil {
		return nil, -1, err
	}
	if waveOrder > 0 {
		for i, group := range target.Groups {
			if group != nil && group.WaveOrder == waveOrder {
				return target, i, nil
			}
		}
		return nil, -1, fmt.Errorf("release rollout %s has no wave %d", rolloutID, waveOrder)
	}
	for i, group := range target.Groups {
		if group == nil || releaseWaveStatus(group) != ReleaseWavePlanned {
			continue
		}
		return target, i, nil
	}
	return nil, -1, fmt.Errorf("release rollout %s has no planned wave to gate", rolloutID)
}

func persistReleasePlan(project Project, plan *ReleasePlan) (string, error) {
	if plan == nil {
		return "", fmt.Errorf("missing release plan")
	}
	if err := os.MkdirAll(project.LogsDir, 0o755); err != nil {
		return "", fmt.Errorf("create harness logs dir: %w", err)
	}
	path := strings.TrimSpace(plan.ReportPath)
	if path == "" {
		path = filepath.Join(project.LogsDir, plan.BatchID+"-release.json")
	}
	previous, err := loadReleasePlanSnapshot(path)
	if err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal release plan: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("write release plan: %w", err)
	}
	plan.ReportPath = path
	if err := recordReleasePlanEvent(project, previous, plan); err != nil {
		return "", fmt.Errorf("record release event: %w", err)
	}
	return path, nil
}

func loadReleasePlans(project Project) ([]*ReleasePlan, error) {
	entries, err := os.ReadDir(project.LogsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read harness logs dir: %w", err)
	}
	var plans []*ReleasePlan
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "-release.json") {
			continue
		}
		path := filepath.Join(project.LogsDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read release report %s: %w", entry.Name(), err)
		}
		var plan ReleasePlan
		if err := json.Unmarshal(data, &plan); err != nil {
			return nil, fmt.Errorf("decode release report %s: %w", entry.Name(), err)
		}
		plan.ReportPath = path
		plans = append(plans, &plan)
	}
	return plans, nil
}
