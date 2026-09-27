package im

// Localization tables for localizedToolActivity, extracted MECHANICALLY from
// the pristine inline switches (r169, tool_status.go) - no hand transcription.
// Output is pinned byte-for-byte by TestLocalizedToolActivityGolden.
//
// toolActivityNoTarget* : fixed activity text when the tool target is empty.
// toolActivityWithTarget* : target prefix text, concatenated with the target
// (mirrors the pristine "prefix" + target form). Note the deliberate
// absence of a "todo" entry in the with-target tables (todo calls never
// carry a target; todo+target falls through to localizedGenericActivity).

var toolActivityNoTargetZh = map[string]string{
	"read":    "读取文件",
	"edit":    "编辑文件",
	"create":  "创建文件",
	"write":   "写入文件",
	"search":  "搜索中...",
	"find":    "查找文件",
	"list":    "列出目录",
	"run":     "执行命令",
	"fetch":   "抓取网页",
	"todo":    "更新待办",
	"task":    "执行任务",
	"skill":   "加载技能",
	"ask":     "等待用户输入",
	"inspect": "检查中...",
}

var toolActivityNoTargetEn = map[string]string{
	"read":    "Reading file",
	"edit":    "Editing file",
	"create":  "Creating file",
	"write":   "Writing file",
	"search":  "Searching...",
	"find":    "Finding files",
	"list":    "Listing directory",
	"run":     "Running command",
	"fetch":   "Fetching page",
	"todo":    "Updating todos",
	"task":    "Running task",
	"skill":   "Loading skill",
	"ask":     "Waiting for user input",
	"inspect": "Inspecting...",
}

var toolActivityWithTargetZh = map[string]string{
	"read":    "读取 ",
	"edit":    "编辑 ",
	"create":  "创建 ",
	"write":   "写入 ",
	"search":  "搜索 ",
	"find":    "查找 ",
	"list":    "列出 ",
	"run":     "执行 ",
	"fetch":   "抓取 ",
	"task":    "执行任务 ",
	"skill":   "加载技能 ",
	"ask":     "提问 ",
	"inspect": "检查 ",
}

var toolActivityWithTargetEn = map[string]string{
	"read":    "Reading ",
	"edit":    "Editing ",
	"create":  "Creating ",
	"write":   "Writing ",
	"search":  "Searching ",
	"find":    "Finding ",
	"list":    "Listing ",
	"run":     "Running ",
	"fetch":   "Fetching ",
	"task":    "Running task ",
	"skill":   "Loading skill ",
	"ask":     "Asking ",
	"inspect": "Inspecting ",
}
