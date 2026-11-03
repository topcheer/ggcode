package agent

import "fmt"

func finalTurnEvidenceGate(editsThisRun int, lastSourceFile string, realBuildOrTestRun, gateAlreadyFired bool, workingDir string) string {
	if gateAlreadyFired || editsThisRun == 0 || realBuildOrTestRun {
		return ""
	}
	fallback := detectBuildSystem(workingDir)
	if fallback == "" {

		return ""
	}
	cmd := targetedVerifyCommand(workingDir, lastSourceFile)
	if cmd == "" {

		cmd = fallback
	}
	return fmt.Sprintf(
		"[Evidence Gate] You edited %d source file(s) this run but have not executed any "+
			"build/test/verify command. Do not claim the work is done - run `%s` first and "+
			"report the actual result. If it fails, fix it. (This gate fires once per run; "+
			"stopping again without evidence will be allowed but the claim is unsupported.)",
		editsThisRun, cmd)
}
