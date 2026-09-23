package tui

import (
	"strconv"
	"time"
)

// describeCronFamilyTool renders timers and scheduled job management tools.
func describeCronFamilyTool(lang Language, toolName string, args map[string]any, fileTarget string) toolPresentation {
	switch toolName {
	case "sleep":
		sec, _ := strconv.Atoi(argString(args, "seconds"))
		ms, _ := strconv.Atoi(argString(args, "milliseconds"))
		d := time.Duration(sec)*time.Second + time.Duration(ms)*time.Millisecond
		if d <= 0 {
			d = 0
		}
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "sleep"),
			Detail:      d.String(),
			Activity:    localizedToolActivity(lang, "sleep", d.String()),
		}
	case "cron_create":
		cronExpr := argString(args, "cron")
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "cron_create"),
			Detail:      cronExpr,
			Activity:    localizedToolActivity(lang, "cron_create", cronExpr),
		}
	case "cron_delete":
		return toolPresentationFor(lang, "delete", "cron job")
	case "cron_list":
		return toolPresentationFor(lang, "inspect", "cron jobs")
	case "cron_update":
		jobID := argString(args, "jobId")
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "cron_update"),
			Detail:      jobID,
			Activity:    localizedToolActivity(lang, "cron_update", jobID),
		}
	case "cron_pause":
		jobID := argString(args, "jobId")
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "cron_pause"),
			Detail:      jobID,
			Activity:    localizedToolActivity(lang, "cron_pause", jobID),
		}
	case "cron_resume":
		jobID := argString(args, "jobId")
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "cron_resume"),
			Detail:      jobID,
			Activity:    localizedToolActivity(lang, "cron_resume", jobID),
		}
	case "cron_get":
		jobID := argString(args, "jobId")
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "cron_get"),
			Detail:      jobID,
			Activity:    localizedToolActivity(lang, "cron_get", jobID),
		}
	default:
		return describeUnknownFamilyTool(lang, toolName, args, fileTarget)
	}
}
