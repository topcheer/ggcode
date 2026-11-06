package agent

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	sentimentEscalationSoft   = 1
	sentimentEscalationStrong = 2
	sentimentEscalationMax    = 3
)

type userSentimentState struct {
	mu sync.Mutex

	consecutiveNegatives int

	totalCorrections int

	lastDetectedCategory string
}

func newUserSentimentState() *userSentimentState {
	return &userSentimentState{}
}

func (s *userSentimentState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.consecutiveNegatives = 0
}

const (
	negCatRejection   = "rejection"
	negCatFrustration = "frustration"
	negCatRedirection = "redirection"
)

var sentimentPositiveWhitelist = []string{
	"no problem", "no problems", "no worries", "no issues", "no issue", "no need",

	"no sweat", "no complaints", "no concerns", "no biggie", "no rush",
	"not bad", "not too bad", "looks good", "looks great", "looks fine",
	"all good", "well done", "good job", "nice work", "great work",
	"perfect", "excellent", "awesome", "thank you", "thanks",
	"how do i stop", "how to stop", "how can i stop", "how do you stop",
	"how do i restart", "how to restart",

	"没问题", "不碍事", "谢谢", "好的", "很好", "不错", "可以了", "太好了",
	"干得好", "看起来不错", "完美", "棒",

	"计算了", "预算了", "结算", "打算了",
	"垃圾回收", "没用上", "没用到", "没用过", "不对齐",
	"帮我停一下", "我是说中文", "我是说英文", "我是说英语",

	// #5130 Face 2 + #4181 reconciliation: bare 还是错误 ("still wrong")
	// is genuine negative feedback and must fire via the 还是错 pattern -
	// but the compound noun readings 还是错误处理 ("error handling instead")
	// and 还是错误码 ("error code still...") are neutral (#4181 pins), so
	// the allowlist protects exactly those compounds, not the bare phrase.

	"还是错误处理", "还是错误码",

	"区别动", "重庆来",
}

func containsCJKANotA(s string) bool {
	r := []rune(s)
	for i, c := range r {
		if c != '不' {
			continue
		}
		for k := 1; k <= 3 && i-k >= 0 && i+k < len(r); k++ {
			if runesEqual(r[i-k:i], r[i+1:i+1+k]) {
				return true
			}
		}
	}
	return false
}

func patternHasCJK(pat string) bool {
	for _, r := range pat {
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
	}
	return false
}

func isCJKQuestionLine(line string) bool {
	t := strings.TrimRight(line, "。！!，, \t")
	if t == "" {
		return false
	}
	last, _ := utf8.DecodeLastRuneInString(t)
	switch last {
	case '？', '?', '吗', '呢':
		return true
	}
	return false
}

func isEnglishQuestionLine(line string) bool {

	t := strings.ToLower(strings.TrimSpace(line))
	if t == "" {
		return false
	}
	if strings.HasSuffix(strings.TrimRight(t, "! \t"), "?") || strings.HasSuffix(strings.TrimRight(t, "! \t"), "？") {
		return true
	}
	for _, w := range []string{
		"is ", "are ", "was ", "were ",
		"do ", "does ", "did ",
		"why ", "what ", "how ", "when ", "where ", "who ",
		"can ", "could ", "should ", "would ", "will ", "shall ",
		"has ", "have ", "had ",
		"isn't ", "aren't ", "doesn't ", "don't ",
	} {
		if strings.HasPrefix(t, w) {
			return true
		}
	}
	return false
}

func runesEqual(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func isCJKANotAQuestion(line string) bool {
	if utf8.RuneCountInString(line) > 40 || !containsCJKANotA(line) {
		return false
	}
	t := strings.TrimRight(line, "。！!，, ")
	return strings.HasSuffix(t, "？") || strings.HasSuffix(t, "?") ||
		strings.HasSuffix(t, "吗") || strings.HasSuffix(t, "呢")
}

var negFeedbackPatterns = []struct {
	patterns []string
	category string
}{

	{[]string{"no", "nope", "wrong", "incorrect", "not right", "not what", "not correct",
		"that's wrong", "thats wrong", "this is wrong", "bad", "doesn't work",
		"doesnt work", "didnt work", "didn't work", "broken", "still broken",
		"still failing", "still not working", "still doesn't", "still doesnt",
		"not working", "fails", "failing", "error again",

		"错了", "不对", "不正确", "不是这样", "不是我要的", "改错了",
		"坏了", "坏掉了", "不行", "不能用", "没成功", "又错了",
		"还是错", "还是不对", "还是不行", "又失败"},
		negCatRejection},

	{[]string{"instead", "actually", "i meant", "i should have", "let me rephrase",
		"let me clarify", "what i actually", "what i really", "on second thought",
		"never mind", "nevermind", "ignore that", "forget that", "disregard",
		"start over", "try again", "redo",

		"重新来", "重来", "重新开始", "再来一次", "换个", "改用", "换成",
		"我是说", "我的意思是", "我是想要", "算了", "别管了", "忽略刚才"},
		negCatRedirection},

	{[]string{"ugh", "wtf", "this is frustrating", "frustrating",
		"why are you doing this", "why are you doing that", "why do you still",
		"you keep doing", "you keep making", "you keep trying",
		"stop doing", "stop trying",

		"i already told you",
		"are you listening", "read my message", "pay attention",
		"this doesn't make sense", "this doesnt make sense",
		"nonsense", "garbage", "useless", "terrible",

		"停下", "停一下", "别改了", "别动", "烦死了", "太差了", "垃圾",
		"没用", "我说过了", "说过多少次", "你听了吗", "看我的消息"},
		negCatFrustration},
}

func detectNegativeFeedback(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}

	firstLine := message
	if idx := strings.IndexByte(message, '\n'); idx >= 0 {
		firstLine = strings.TrimSpace(message[:idx])
	}

	lower := strings.ToLower(firstLine)

	isShort := utf8.RuneCountInString(firstLine) < 100

	scanText := maskPositivePhrases(lower)

	if isBareStopCommand(lower) {
		return negCatFrustration
	}
	for _, group := range negFeedbackPatterns {

		if group.category == negCatRejection && isCJKANotAQuestion(lower) {
			continue
		}
		for _, pat := range group.patterns {
			if wordContains(scanText, pat) {

				if patternHasCJK(pat) {
					if isCJKQuestionLine(firstLine) {
						continue
					}
				} else if group.category == negCatRejection && isEnglishQuestionLine(firstLine) {

					continue
				}

				if isShort {
					return group.category
				}

				openerWindow := 40
				if group.category != negCatRedirection {
					openerWindow = 12
				}

				if idx := wordIndexIn([]byte(scanText), pat); idx >= 0 && utf8.RuneCountInString(scanText[:idx]) < openerWindow {
					return group.category
				}
			}
		}
	}

	return ""
}

func isBareStopCommand(lower string) bool {
	return strings.Trim(lower, " !.!~\t\n") == "stop"
}

func maskPositivePhrases(text string) string {
	b := []byte(text)
	for _, w := range sentimentPositiveWhitelist {
		for {
			idx := wordIndexIn(b, w)
			if idx < 0 {
				break
			}
			for i := idx; i < idx+len(w); i++ {
				b[i] = ' '
			}
		}
	}
	return string(b)
}

func wordIndexIn(b []byte, pattern string) int {
	pb := []byte(pattern)
	for from := 0; from+len(pb) <= len(b); {
		rel := bytes.Index(b[from:], pb)
		if rel < 0 {
			return -1
		}
		idx := from + rel
		beforeOK := idx == 0 || !isAlnum(b[idx-1])
		afterIdx := idx + len(pb)
		afterOK := afterIdx >= len(b) || !isAlnum(b[afterIdx])
		if beforeOK && afterOK {
			return idx
		}
		from = idx + 1
	}
	return -1
}

func wordContains(text, pattern string) bool {
	idx := strings.Index(text, pattern)
	for idx >= 0 {

		beforeOK := idx == 0 || !isAlnum(text[idx-1])

		afterIdx := idx + len(pattern)
		afterOK := afterIdx >= len(text) || !isAlnum(text[afterIdx])
		if beforeOK && afterOK {
			return true
		}

		next := strings.Index(text[idx+1:], pattern)
		if next < 0 {
			break
		}
		idx = idx + 1 + next
	}
	return false
}

func isAlnum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '\'' || b == '-' || b == '_'
}

type SentimentFeedback struct {
	Level    int
	Category string
}

func (s *userSentimentState) analyzeAndUpdate(message string) SentimentFeedback {
	s.mu.Lock()
	defer s.mu.Unlock()

	category := detectNegativeFeedback(message)
	if category == "" {

		s.consecutiveNegatives = 0
		return SentimentFeedback{Level: 0}
	}

	s.consecutiveNegatives++
	s.totalCorrections++
	s.lastDetectedCategory = category

	level := s.consecutiveNegatives
	if level > sentimentEscalationMax {
		level = sentimentEscalationMax
	}

	debug.Log("agent", "negative user feedback detected: category=%s consecutive=%d level=%d",
		category, s.consecutiveNegatives, level)

	return SentimentFeedback{Level: level, Category: category}
}

func buildSentimentGuidance(fb SentimentFeedback) string {
	if fb.Level == 0 {
		return ""
	}

	var b strings.Builder

	switch fb.Level {
	case sentimentEscalationSoft:
		b.WriteString("The user's last message indicates your previous approach may have been incorrect or unwanted")
		b.WriteString(tailoredSuffix(fb.Category, false))
		b.WriteString("\n\nDo NOT repeat your previous approach. ")
		b.WriteString("Re-read the relevant files to understand their current state, ")
		b.WriteString("and consider a different strategy.")

	case sentimentEscalationStrong:
		b.WriteString("The user has expressed negative feedback multiple times in a row. ")
		b.WriteString("Your recent work is clearly not meeting expectations")
		b.WriteString(tailoredSuffix(fb.Category, false))
		b.WriteString("\n\nSTOP and reassess:\n")
		b.WriteString("1. Re-read the user's ORIGINAL request carefully\n")
		b.WriteString("2. Re-read all files you modified to understand the current state\n")
		b.WriteString("3. Identify what specifically went wrong in your previous approach\n")
		b.WriteString("4. Propose a fundamentally different solution")

	case sentimentEscalationMax:
		b.WriteString("The user has expressed repeated frustration. ")
		b.WriteString("Do NOT make any more changes without first confirming your understanding.\n\n")
		b.WriteString("Use ask_user to:\n")
		b.WriteString("- Summarize what you THINK the user wants\n")
		b.WriteString("- Explain what went wrong with your previous attempts\n")
		b.WriteString("- Ask for explicit confirmation before proceeding\n")
		b.WriteString("Do not continue making edits until the user confirms.")
	}

	return b.String()
}

func tailoredSuffix(category string, _ bool) string {
	switch category {
	case negCatRejection:
		return " (rejection of output)."
	case negCatFrustration:
		return " (frustration signal)."
	case negCatRedirection:
		return " (direction change)."
	default:
		return "."
	}
}

func shouldResetMonitoringOnFeedback(fb SentimentFeedback) bool {
	return fb.Level >= sentimentEscalationStrong
}

func formatSentimentLogLine(fb SentimentFeedback) string {
	return fmt.Sprintf("sentiment feedback: level=%d category=%s", fb.Level, fb.Category)
}
