//go:build windows

package install

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// EnsureOnPath appends dir to the USER Path environment variable
// (powershell setx semantics, idempotent) - the Go port of the python
// installer's ensure_windows_user_path (#1573-A).
func EnsureOnPath(dir string) (bool, error) {
	escaped := strings.ReplaceAll(dir, "'", "''")
	script := "$dir = '" + escaped + "'; " +
		"$current = [Environment]::GetEnvironmentVariable('Path', 'User'); " +
		"$parts = @(); " +
		"if ($current) { $parts = $current -split ';' | Where-Object { $_ -and $_.Trim() -ne '' } }; " +
		"$exists = $parts | Where-Object { $_.TrimEnd('\\') -ieq $dir.TrimEnd('\\') }; " +
		"if (-not $exists) { " +
		"  $new = @($dir) + $parts; " +
		"  [Environment]::SetEnvironmentVariable('Path', ($new -join ';'), 'User'); " +
		"  Write-Output 'updated' " +
		"} else { Write-Output 'unchanged' }"
	out, err := exec.Command("powershell", "-NoProfile", "-Command", script).CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("updating user PATH: %w: %s", err, strings.TrimSpace(string(out)))
	}
	changed := strings.TrimSpace(string(out)) == "updated"
	if changed {
		debug.Log("install", "path: user PATH updated with %s", dir)
	}
	return changed, nil
}
