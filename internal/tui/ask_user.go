package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

type questionnaireState struct {
	request      toolpkg.AskUserRequest
	response     chan toolpkg.AskUserResponse
	tabIndex     int
	choiceCursor int
	input        textinput.Model
	answers      []questionnaireAnswerState
}

type questionnaireAnswerState struct {
	selected map[string]struct{}
	freeform string
}

func newQuestionnaireState(req toolpkg.AskUserRequest, response chan toolpkg.AskUserResponse, lang Language) *questionnaireState {
	input := newQuestionnaireInput(lang)
	state := &questionnaireState{
		request:  req,
		response: response,
		input:    input,
		answers:  make([]questionnaireAnswerState, len(req.Questions)),
	}
	for i := range state.answers {
		state.answers[i].selected = make(map[string]struct{})
	}
	state.loadActiveQuestion(lang)
	return state
}

func newQuestionnaireInput(lang Language) textinput.Model {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = questionnaireFreeformPlaceholder(lang)
	input.Focus()
	return input
}

func (m *Model) syncQuestionnaireInputWidth() {
	if m.pendingQuestionnaire == nil {
		return
	}
	width := m.boxInnerWidth(m.mainColumnWidth()) - 4
	if width < 20 {
		width = 20
	}
	m.pendingQuestionnaire.input.Width = width
}

func (m Model) handleQuestionnaireKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	qs := m.pendingQuestionnaire
	if qs == nil {
		return m, nil
	}
	switch msg.Type {
	case tea.KeyTab, tea.KeyRight:
		qs.moveTab(1, m.currentLanguage())
		return m, nil
	case tea.KeyShiftTab, tea.KeyLeft:
		qs.moveTab(-1, m.currentLanguage())
		return m, nil
	case tea.KeyEnter:
		if qs.onSubmitTab() {
			return m, m.handleQuestionnaireResult(toolpkg.AskUserStatusSubmitted)
		}
		if qs.onCancelTab() {
			return m, m.handleQuestionnaireResult(toolpkg.AskUserStatusCancelled)
		}
		qs.moveTab(1, m.currentLanguage())
		return m, nil
	case tea.KeyEsc, tea.KeyCtrlC:
		return m, m.handleQuestionnaireResult(toolpkg.AskUserStatusCancelled)
	case tea.KeyUp:
		if qs.activeQuestionHasChoices() {
			qs.moveChoice(-1)
		}
		return m, nil
	case tea.KeyDown:
		if qs.activeQuestionHasChoices() {
			qs.moveChoice(1)
		}
		return m, nil
	case tea.KeySpace:
		if qs.activeQuestionHasChoices() {
			qs.toggleCurrentChoice()
		}
		return m, nil
	}
	switch msg.String() {
	case "k":
		if qs.activeQuestionHasChoices() {
			qs.moveChoice(-1)
		}
		return m, nil
	case "j":
		if qs.activeQuestionHasChoices() {
			qs.moveChoice(1)
		}
		return m, nil
	}
	if qs.activeQuestionAllowsFreeform() {
		var cmd tea.Cmd
		qs.input, cmd = qs.input.Update(msg)
		qs.saveActiveQuestionInput()
		return m, cmd
	}
	return m, nil
}

func (m *Model) handleQuestionnaireResult(status string) tea.Cmd {
	qs := m.pendingQuestionnaire
	m.pendingQuestionnaire = nil
	if qs == nil || qs.response == nil {
		return nil
	}
	response := qs.buildResponse(status)
	go func() {
		qs.response <- response
	}()
	return nil
}

func (m Model) renderQuestionnairePanel() string {
	qs := m.pendingQuestionnaire
	if qs == nil {
		return ""
	}
	lang := m.currentLanguage()
	body := strings.Builder{}
	if title := strings.TrimSpace(qs.request.Title); title != "" {
		body.WriteString(lipgloss.NewStyle().Bold(true).Render(title))
		body.WriteString("\n\n")
	}
	body.WriteString(m.renderQuestionnaireTabs())
	body.WriteString("\n\n")
	if qs.onSubmitTab() || qs.onCancelTab() {
		body.WriteString(m.renderQuestionnaireActionBody())
		body.WriteString("\n\n")
		body.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(questionnaireActionHint(lang)))
	} else {
		body.WriteString(m.renderQuestionnaireQuestionBody())
		body.WriteString("\n\n")
		body.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(questionnaireQuestionHint(lang, qs.activeQuestionAllowsFreeform())))
	}
	return m.renderContextBox(questionnairePanelTitle(lang), body.String(), lipgloss.Color("14"))
}

func (m Model) renderQuestionnaireTabs() string {
	qs := m.pendingQuestionnaire
	if qs == nil {
		return ""
	}
	tabs := make([]string, 0, len(qs.request.Questions)+2)
	for i, question := range qs.request.Questions {
		label := fmt.Sprintf("%s %s", questionnaireStateBadge(qs.answerCompletionStatus(i)), truncateString(strings.TrimSpace(question.Title), 18))
		tabs = append(tabs, m.renderQuestionnaireTab(label, qs.tabIndex == i))
	}
	tabs = append(tabs, m.renderQuestionnaireTab(questionnaireSubmitLabel(m.currentLanguage()), qs.onSubmitTab()))
	tabs = append(tabs, m.renderQuestionnaireTab(questionnaireCancelLabel(m.currentLanguage()), qs.onCancelTab()))
	return lipgloss.JoinHorizontal(lipgloss.Top, tabs...)
}

func (m Model) renderQuestionnaireTab(label string, active bool) string {
	style := lipgloss.NewStyle().
		Padding(0, 1).
		MarginRight(1).
		Border(lipgloss.RoundedBorder())
	if active {
		style = style.BorderForeground(lipgloss.Color("14")).Bold(true)
	} else {
		style = style.BorderForeground(lipgloss.Color("8")).Foreground(lipgloss.Color("8"))
	}
	return style.Render(label)
}

func (m Model) renderQuestionnaireQuestionBody() string {
	qs := m.pendingQuestionnaire
	if qs == nil {
		return ""
	}
	idx := qs.activeQuestionIndex()
	if idx < 0 || idx >= len(qs.request.Questions) {
		return ""
	}
	question := qs.request.Questions[idx]
	lines := []string{
		lipgloss.NewStyle().Bold(true).Render(question.Prompt),
		lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(
			fmt.Sprintf("%s %d/%d", questionnaireSummaryLabel(m.currentLanguage()), qs.answeredCount(), len(qs.request.Questions)),
		),
	}
	if len(question.Choices) > 0 {
		lines = append(lines, "")
		lines = append(lines, m.renderQuestionnaireChoices(idx))
	}
	if question.AllowFreeform {
		lines = append(lines, "")
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(questionnaireFreeformLabel(m.currentLanguage())))
		lines = append(lines, m.pendingQuestionnaire.input.View())
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderQuestionnaireChoices(questionIndex int) string {
	qs := m.pendingQuestionnaire
	question := qs.request.Questions[questionIndex]
	answer := qs.answers[questionIndex]
	lines := make([]string, 0, len(question.Choices))
	for i, choice := range question.Choices {
		cursor := " "
		if qs.choiceCursor == i {
			cursor = ">"
		}
		mark := " "
		if _, ok := answer.selected[choice.ID]; ok {
			mark = "x"
		}
		line := fmt.Sprintf(" %s [%s] %s", cursor, mark, choice.Label)
		if qs.choiceCursor == i {
			line = m.styles.approvalCursor.Render(line)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderQuestionnaireActionBody() string {
	qs := m.pendingQuestionnaire
	if qs == nil {
		return ""
	}
	var title, body string
	if qs.onSubmitTab() {
		title = questionnaireSubmitTitle(m.currentLanguage())
		body = questionnaireSubmitBody(m.currentLanguage(), qs.answeredCount(), len(qs.request.Questions))
	} else {
		title = questionnaireCancelTitle(m.currentLanguage())
		body = questionnaireCancelBody(m.currentLanguage())
	}
	lines := []string{
		lipgloss.NewStyle().Bold(true).Render(title),
		body,
		"",
	}
	for i, question := range qs.request.Questions {
		lines = append(lines, fmt.Sprintf(" %s %s — %s",
			questionnaireStateBadge(qs.answerCompletionStatus(i)),
			question.Title,
			questionnaireCompletionLabel(m.currentLanguage(), qs.answerCompletionStatus(i)),
		))
	}
	return strings.Join(lines, "\n")
}

func (qs *questionnaireState) activeQuestionIndex() int {
	if qs == nil || qs.tabIndex >= len(qs.request.Questions) {
		return -1
	}
	return qs.tabIndex
}

func (qs *questionnaireState) onSubmitTab() bool {
	return qs != nil && qs.tabIndex == len(qs.request.Questions)
}

func (qs *questionnaireState) onCancelTab() bool {
	return qs != nil && qs.tabIndex == len(qs.request.Questions)+1
}

func (qs *questionnaireState) activeQuestionHasChoices() bool {
	idx := qs.activeQuestionIndex()
	return idx >= 0 && len(qs.request.Questions[idx].Choices) > 0
}

func (qs *questionnaireState) activeQuestionAllowsFreeform() bool {
	idx := qs.activeQuestionIndex()
	return idx >= 0 && qs.request.Questions[idx].AllowFreeform
}

func (qs *questionnaireState) moveTab(delta int, lang Language) {
	if qs == nil {
		return
	}
	qs.saveActiveQuestionInput()
	total := len(qs.request.Questions) + 2
	qs.tabIndex = (qs.tabIndex + delta + total) % total
	qs.loadActiveQuestion(lang)
}

func (qs *questionnaireState) moveChoice(delta int) {
	idx := qs.activeQuestionIndex()
	if idx < 0 {
		return
	}
	choices := qs.request.Questions[idx].Choices
	if len(choices) == 0 {
		return
	}
	qs.choiceCursor = (qs.choiceCursor + delta + len(choices)) % len(choices)
}

func (qs *questionnaireState) toggleCurrentChoice() {
	idx := qs.activeQuestionIndex()
	if idx < 0 {
		return
	}
	question := qs.request.Questions[idx]
	if len(question.Choices) == 0 || qs.choiceCursor < 0 || qs.choiceCursor >= len(question.Choices) {
		return
	}
	choiceID := question.Choices[qs.choiceCursor].ID
	answer := &qs.answers[idx]
	if question.Kind == toolpkg.AskUserKindSingle {
		if _, already := answer.selected[choiceID]; already {
			delete(answer.selected, choiceID)
			return
		}
		answer.selected = map[string]struct{}{choiceID: {}}
		return
	}
	if _, already := answer.selected[choiceID]; already {
		delete(answer.selected, choiceID)
		return
	}
	answer.selected[choiceID] = struct{}{}
}

func (qs *questionnaireState) loadActiveQuestion(lang Language) {
	idx := qs.activeQuestionIndex()
	if idx < 0 {
		qs.input.Blur()
		qs.input.SetValue("")
		return
	}
	question := qs.request.Questions[idx]
	qs.choiceCursor = 0
	qs.input.Placeholder = firstNonEmpty(strings.TrimSpace(question.Placeholder), questionnaireFreeformPlaceholder(lang))
	qs.input.SetValue(qs.answers[idx].freeform)
	if question.AllowFreeform {
		qs.input.Focus()
	} else {
		qs.input.Blur()
	}
}

func (qs *questionnaireState) saveActiveQuestionInput() {
	idx := qs.activeQuestionIndex()
	if idx < 0 {
		return
	}
	qs.answers[idx].freeform = qs.input.Value()
}

func (qs *questionnaireState) buildResponse(status string) toolpkg.AskUserResponse {
	qs.saveActiveQuestionInput()
	answers := make([]toolpkg.AskUserAnswer, 0, len(qs.request.Questions))
	answeredCount := 0
	for i, question := range qs.request.Questions {
		answer := qs.buildAnswer(i, question)
		if answer.Answered {
			answeredCount++
		}
		answers = append(answers, answer)
	}
	return toolpkg.AskUserResponse{
		Status:        status,
		Title:         qs.request.Title,
		QuestionCount: len(qs.request.Questions),
		AnsweredCount: answeredCount,
		Answers:       answers,
	}
}

func (qs *questionnaireState) buildAnswer(index int, question toolpkg.AskUserQuestion) toolpkg.AskUserAnswer {
	answerState := qs.answers[index]
	selectedIDs := make([]string, 0, len(answerState.selected))
	selectedLabels := make([]string, 0, len(answerState.selected))
	for _, choice := range question.Choices {
		if _, ok := answerState.selected[choice.ID]; ok {
			selectedIDs = append(selectedIDs, choice.ID)
			selectedLabels = append(selectedLabels, choice.Label)
		}
	}
	freeform := strings.TrimSpace(answerState.freeform)
	answerMode := toolpkg.AskUserAnswerModeNone
	completionStatus := toolpkg.AskUserCompletionUnanswered
	switch {
	case len(selectedIDs) == 0 && freeform == "":
		answerMode = toolpkg.AskUserAnswerModeNone
		completionStatus = toolpkg.AskUserCompletionUnanswered
	case len(selectedIDs) == 0 && freeform != "":
		answerMode = toolpkg.AskUserAnswerModeFreeformOnly
		if question.Kind == toolpkg.AskUserKindText {
			completionStatus = toolpkg.AskUserCompletionAnswered
		} else {
			completionStatus = toolpkg.AskUserCompletionPartial
		}
	case len(selectedIDs) > 0 && freeform == "":
		answerMode = toolpkg.AskUserAnswerModeSelectionOnly
		completionStatus = toolpkg.AskUserCompletionAnswered
	default:
		answerMode = toolpkg.AskUserAnswerModeSelectionAndFreeform
		completionStatus = toolpkg.AskUserCompletionAnswered
	}
	return toolpkg.AskUserAnswer{
		ID:                question.ID,
		Title:             question.Title,
		Kind:              question.Kind,
		CompletionStatus:  completionStatus,
		AnswerMode:        answerMode,
		Answered:          completionStatus == toolpkg.AskUserCompletionAnswered,
		SelectedChoiceIDs: selectedIDs,
		SelectedChoices:   selectedLabels,
		FreeformText:      freeform,
	}
}

func (qs *questionnaireState) answerCompletionStatus(index int) string {
	return qs.buildAnswer(index, qs.request.Questions[index]).CompletionStatus
}

func (qs *questionnaireState) answeredCount() int {
	count := 0
	for i, question := range qs.request.Questions {
		if qs.buildAnswer(i, question).Answered {
			count++
		}
	}
	return count
}

func (qs *questionnaireState) firstUnansweredQuestionIndex() int {
	for i, question := range qs.request.Questions {
		if !qs.buildAnswer(i, question).Answered {
			return i
		}
	}
	return -1
}

func (qs *questionnaireState) applyRemoteAnswer(raw string, lang Language) (bool, error) {
	if qs == nil {
		return false, fmt.Errorf("questionnaire unavailable")
	}
	text := strings.TrimSpace(raw)
	if text == "" {
		return false, fmt.Errorf("empty answer")
	}
	idx := qs.firstUnansweredQuestionIndex()
	if idx < 0 {
		idx = qs.activeQuestionIndex()
	}
	if idx < 0 || idx >= len(qs.request.Questions) {
		return false, fmt.Errorf("no active question")
	}
	qs.tabIndex = idx
	qs.loadActiveQuestion(lang)
	answer := &qs.answers[idx]
	question := qs.request.Questions[idx]

	selected, freeform, err := parseRemoteQuestionnaireAnswer(text, question)
	if err != nil {
		return false, err
	}
	if selected != nil {
		answer.selected = selected
	}
	if freeform != "" || question.Kind == toolpkg.AskUserKindText || question.AllowFreeform {
		answer.freeform = freeform
	}
	if qs.answeredCount() >= len(qs.request.Questions) {
		return true, nil
	}
	nextIdx := qs.firstUnansweredQuestionIndex()
	if nextIdx >= 0 {
		qs.tabIndex = nextIdx
		qs.loadActiveQuestion(lang)
	}
	return false, nil
}

func parseRemoteQuestionnaireAnswer(raw string, question toolpkg.AskUserQuestion) (map[string]struct{}, string, error) {
	text := strings.TrimSpace(raw)
	switch question.Kind {
	case toolpkg.AskUserKindText:
		return nil, text, nil
	case toolpkg.AskUserKindSingle, toolpkg.AskUserKindMulti:
		selected, matched, err := parseRemoteQuestionnaireSelections(text, question)
		if err != nil {
			return nil, "", err
		}
		if matched {
			return selected, "", nil
		}
		if question.AllowFreeform {
			return nil, text, nil
		}
		return nil, "", fmt.Errorf("reply with a choice number")
	default:
		if question.AllowFreeform {
			return nil, text, nil
		}
		return nil, "", fmt.Errorf("unsupported question kind %q", question.Kind)
	}
}

func parseRemoteQuestionnaireSelections(raw string, question toolpkg.AskUserQuestion) (map[string]struct{}, bool, error) {
	text := strings.TrimSpace(raw)
	if text == "" || len(question.Choices) == 0 {
		return nil, false, nil
	}
	tokens := strings.FieldsFunc(text, func(r rune) bool {
		switch r {
		case ',', '，', '、', ';', '；', '\n', '\t', ' ':
			return true
		default:
			return false
		}
	})
	if len(tokens) == 0 {
		return nil, false, nil
	}

	allNumeric := true
	selected := make(map[string]struct{})
	for _, token := range tokens {
		n, err := strconv.Atoi(token)
		if err != nil {
			allNumeric = false
			break
		}
		if n < 1 || n > len(question.Choices) {
			return nil, false, fmt.Errorf("choice %d is out of range", n)
		}
		selected[question.Choices[n-1].ID] = struct{}{}
	}
	if allNumeric {
		if question.Kind == toolpkg.AskUserKindSingle && len(selected) > 1 {
			return nil, false, fmt.Errorf("pick one choice")
		}
		return selected, true, nil
	}

	needle := normalizeRemoteAnswerToken(text)
	for _, choice := range question.Choices {
		if normalizeRemoteAnswerToken(choice.ID) == needle || normalizeRemoteAnswerToken(choice.Label) == needle {
			return map[string]struct{}{choice.ID: {}}, true, nil
		}
	}
	return nil, false, nil
}

func normalizeRemoteAnswerToken(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\t", "")
	return s
}

func questionnairePanelTitle(lang Language) string {
	if lang == LangZhCN {
		return "请补充信息"
	}
	return "Answer questions"
}

func questionnaireSubmitLabel(lang Language) string {
	if lang == LangZhCN {
		return "提交"
	}
	return "Submit"
}

func questionnaireCancelLabel(lang Language) string {
	if lang == LangZhCN {
		return "取消"
	}
	return "Cancel"
}

func questionnaireSummaryLabel(lang Language) string {
	if lang == LangZhCN {
		return "已完成"
	}
	return "Answered"
}

func questionnaireFreeformLabel(lang Language) string {
	if lang == LangZhCN {
		return "补充说明"
	}
	return "Notes"
}

func questionnaireFreeformPlaceholder(lang Language) string {
	if lang == LangZhCN {
		return "可选补充说明"
	}
	return "Optional notes"
}

func questionnaireQuestionHint(lang Language, allowFreeform bool) string {
	if lang == LangZhCN {
		if allowFreeform {
			return "Tab 切换标签 • ↑/↓ 移动选项 • Space 选择 • 输入补充说明 • Enter 下一题 • Esc 取消"
		}
		return "Tab 切换标签 • ↑/↓ 移动选项 • Space 选择 • Enter 下一题 • Esc 取消"
	}
	if allowFreeform {
		return "Tab switch tabs • Up/Down move • Space select • Type notes • Enter next • Esc cancel"
	}
	return "Tab switch tabs • Up/Down move • Space select • Enter next • Esc cancel"
}

func questionnaireActionHint(lang Language) string {
	if lang == LangZhCN {
		return "Tab 切换标签 • Enter 确认 • Esc 取消"
	}
	return "Tab switch tabs • Enter confirm • Esc cancel"
}

func questionnaireSubmitTitle(lang Language) string {
	if lang == LangZhCN {
		return "提交答案"
	}
	return "Submit answers"
}

func questionnaireSubmitBody(lang Language, answered, total int) string {
	if lang == LangZhCN {
		return fmt.Sprintf("将当前 %d/%d 个问题的答案一次性提交给 agent。未答项也会一并返回，由模型决定是否继续追问。", answered, total)
	}
	return fmt.Sprintf("Submit the current answers for %d/%d questions in one batch. Unanswered items are still returned so the agent can decide whether to ask again.", answered, total)
}

func questionnaireCancelTitle(lang Language) string {
	if lang == LangZhCN {
		return "取消问答"
	}
	return "Cancel questionnaire"
}

func questionnaireCancelBody(lang Language) string {
	if lang == LangZhCN {
		return "返回一个结构化的 cancelled 结果给 agent，并保留当前已填写的部分答案。"
	}
	return "Return a structured cancelled result to the agent and keep any partial answers in the payload."
}

func questionnaireCompletionLabel(lang Language, status string) string {
	switch status {
	case toolpkg.AskUserCompletionAnswered:
		if lang == LangZhCN {
			return "已回答"
		}
		return "answered"
	case toolpkg.AskUserCompletionPartial:
		if lang == LangZhCN {
			return "部分回答"
		}
		return "partial"
	default:
		if lang == LangZhCN {
			return "未回答"
		}
		return "unanswered"
	}
}

func questionnaireStateBadge(status string) string {
	switch status {
	case toolpkg.AskUserCompletionAnswered:
		return "[x]"
	case toolpkg.AskUserCompletionPartial:
		return "[~]"
	default:
		return "[ ]"
	}
}
