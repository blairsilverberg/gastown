package cmd

import (
	"os"
	"strings"
	"testing"
)

func TestExtractCommand(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"valid hook input", `{"tool_name":"Bash","tool_input":{"command":"rm -rf /tmp/foo"}}`, "rm -rf /tmp/foo"},
		{"empty input", "", ""},
		{"invalid json", "not json", ""},
		{"no command field", `{"tool_name":"Write","tool_input":{"file_path":"/tmp/foo"}}`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractCommand([]byte(tt.input))
			if got != tt.want {
				t.Errorf("extractCommand() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMatchesAllFragments(t *testing.T) {
	tests := []struct {
		name      string
		command   string
		fragments []string
		want      bool
	}{
		{"git reset hard", "git reset --hard", []string{"git", "reset", "--hard"}, true},
		{"git reset soft", "git reset --soft", []string{"git", "reset", "--hard"}, false},
		{"drop table", "drop table users", []string{"drop", "table"}, true},
		{"drop database", "drop database mydb", []string{"drop", "database"}, true},
		{"truncate table", "truncate table logs", []string{"truncate", "table"}, true},
		{"git clean -f", "git clean -f", []string{"git", "clean", "-f"}, true},
		{"git clean -n", "git clean -n", []string{"git", "clean", "-f"}, false},
		{"no match", "echo hello", []string{"rm", "-rf"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesAllFragments(tt.command, tt.fragments)
			if got != tt.want {
				t.Errorf("matchesAllFragments(%q, %v) = %v, want %v", tt.command, tt.fragments, got, tt.want)
			}
		})
	}
}

// withFixtureTownRoot pins the guard's town-root discovery to /home/ubuntu/gt
// and its cwd resolution to a directory outside any town, so the table below
// asserts the PREDICATE and not the machine the test runs on.
func withFixtureTownRoot(t *testing.T) {
	t.Helper()
	prev := townRootsForGuard
	townRootsForGuard = func() []string { return []string{"/home/ubuntu/gt"} }
	t.Cleanup(func() { townRootsForGuard = prev })
	t.Setenv("HOME", "/home/ubuntu")
	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
}

func TestMatchesDangerousRmRf(t *testing.T) {
	tests := []struct {
		name    string
		command string
		blocked bool
	}{
		// Should block
		{"rm -rf /", "rm -rf /", true},
		{"rm -rf /*", "rm -rf /*", true},
		{"rm -rf / with sudo", "sudo rm -rf /", true},

		// Should block — at or above a town root (AA-1019, Blair 2026-08-27).
		// townRootsForGuard is pinned to a fixture below so these do not
		// depend on where the test process happens to be running.
		{"town root", "rm -rf /home/ubuntu/gt", true},
		{"town root trailing slash", "rm -rf /home/ubuntu/gt/", true},
		{"town root glob", "rm -rf /home/ubuntu/gt/*", true},
		{"town root doubled slashes", "rm -rf //home//ubuntu//gt", true},
		{"town root via dot segments", "rm -rf /home/ubuntu/gt/logs/..", true},
		{"town root quoted", "rm -rf \"/home/ubuntu/gt\"", true},
		{"home dir", "rm -rf /home/ubuntu", true},
		{"home dir glob", "rm -rf /home/ubuntu/*", true},
		{"tilde", "rm -rf ~", true},
		{"tilde slash", "rm -rf ~/", true},
		{"HOME var", "rm -rf $HOME", true},
		{"HOME braced var", "rm -rf ${HOME}", true},
		{"/home", "rm -rf /home", true},
		{"flags reversed -fr", "rm -fr /home/ubuntu/gt", true},
		{"flags split -r -f", "rm -r -f /home/ubuntu/gt", true},
		{"long flags", "rm --recursive --force /home/ubuntu/gt", true},
		{"flags bundled -rvf", "rm -rvf /home/ubuntu/gt", true},
		{"target before flags", "rm /home/ubuntu/gt -rf", true},
		{"literal assignment indirection", "T=/home/ubuntu/gt; rm -rf $T", true},
		{"cd then relative target", "cd /home/ubuntu && rm -rf gt", true},
		{"cd then dot", "cd /home/ubuntu/gt && rm -rf .", true},
		{"cd then bare glob", "cd /home/ubuntu/gt && rm -rf *", true},
		{"sudo town root", "sudo rm -rf /home/ubuntu/gt", true},

		// Should allow (normal cleanup commands)
		{"rm -rf ./build/", "rm -rf ./build/", false},
		{"below town root", "rm -rf /home/ubuntu/gt/logs", false},
		{"below town root glob", "rm -rf /home/ubuntu/gt/logs/*", false},
		{"below home", "rm -rf /home/ubuntu/scratch", false},
		{"tilde below home", "rm -rf ~/scratch/build", false},
		{"sibling of town root", "rm -rf /home/ubuntu/gastown-src/dist", false},
		{"git rm -r --cached at town root", "cd /home/ubuntu/gt && git rm -r --cached .", false},
		{"cd below town root then dot", "cd /home/ubuntu/gt/logs && rm -rf .", false},
		{"rm -rf node_modules/", "rm -rf node_modules/", false},
		{"rm -rf /tmp/test-output/", "rm -rf /tmp/test-output/", false},
		{"rm -rf relative dir", "rm -rf build", false},
		{"rm single file", "rm foo.txt", false},
		// STRENGTHENED 2026-08-27 (AA-1019). This case previously expected
		// false: the old predicate required -f. Force is no longer required
		// for a target at or above a town root, because GNU rm deletes a
		// writable tree without it. Expectation flipped, not removed.
		{"rm -r / (recursive, no force)", "rm -r /", true},
		{"no rm at all", "echo hello", false},
	}
	withFixtureTownRoot(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesDangerousRmRf(tt.command) != ""
			if got != tt.blocked {
				t.Errorf("matchesDangerousRmRf(%q) blocked=%v, want %v", tt.command, got, tt.blocked)
			}
		})
	}
}

func TestMatchesDangerousGitPush(t *testing.T) {
	tests := []struct {
		name    string
		command string
		blocked bool
	}{
		// Should block
		{"git push --force", "git push --force origin main", true},
		{"git push -f", "git push -f origin main", true},
		{"git push --force bare", "git push --force", true},

		// Should allow (safe variants)
		{"force-with-lease", "git push --force-with-lease origin main", false},
		{"force-if-includes", "git push --force-if-includes origin main", false},
		{"normal push", "git push origin main", false},
		{"no push", "git status", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesDangerousGitPush(tt.command) != ""
			if got != tt.blocked {
				t.Errorf("matchesDangerousGitPush(%q) blocked=%v, want %v", tt.command, got, tt.blocked)
			}
		})
	}
}

func TestMatchesSudo(t *testing.T) {
	tests := []struct {
		name    string
		command string
		blocked bool
	}{
		// Should block
		{"sudo dnf install", "sudo dnf install -y postgresql-contrib", true},
		{"sudo rm", "sudo rm -rf /var/log/syslog", true},
		{"sudo bare", "sudo su", true},
		{"sudo in pipeline", "echo foo | sudo tee /etc/config", true},

		// Should allow
		{"no sudo", "echo hello", false},
		{"sudo in string", "echo 'do not use sudo'", false}, // contains "sudo" as substring of different token? Actually "sudo" IS a token here
		{"pseudocode", "cat pseudocode.txt", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesSudo(strings.ToLower(tt.command)) != ""
			if got != tt.blocked {
				t.Errorf("matchesSudo(%q) blocked=%v, want %v", tt.command, got, tt.blocked)
			}
		})
	}
}

func TestMatchesPackageInstall(t *testing.T) {
	tests := []struct {
		name    string
		command string
		blocked bool
	}{
		// Should block
		{"apt install", "apt install -y curl", true},
		{"apt-get install", "apt-get install -y build-essential", true},
		{"dnf install", "dnf install -y postgresql-contrib", true},
		{"yum install", "yum install -y gcc", true},
		{"pacman -S", "pacman -S git", true},
		{"brew install", "brew install node", true},
		{"gem install", "gem install bundler", true},
		{"pip install --system", "pip install --system requests", true},
		{"pip3 install --system", "pip3 install --system flask", true},
		{"npm install -g", "npm install -g typescript", true},
		{"npm install --global", "npm install --global eslint", true},

		// Should allow
		{"pip install (venv ok)", "pip install requests", false},
		{"npm install (local ok)", "npm install express", false},
		{"npm install --save-dev", "npm install --save-dev jest", false},
		{"go install", "go install ./...", false},
		{"cargo install", "cargo install ripgrep", false},
		{"normal command", "ls -la", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesPackageInstall(strings.ToLower(tt.command)) != ""
			if got != tt.blocked {
				t.Errorf("matchesPackageInstall(%q) blocked=%v, want %v", tt.command, got, tt.blocked)
			}
		})
	}
}

// TestDangerousGuard_Integration tests the full pattern set end-to-end.
func TestDangerousGuard_Integration(t *testing.T) {
	tests := []struct {
		name    string
		command string
		blocked bool
	}{
		// Blocked — privilege escalation
		{"sudo command", "sudo dnf install -y foo", true},
		{"sudo rm", "sudo rm -rf /var/cache", true},

		// Blocked — package installs
		{"apt install", "apt install -y curl", true},
		{"dnf install", "dnf install -y postgresql-contrib", true},
		{"brew install", "brew install node", true},
		{"npm install -g", "npm install -g typescript", true},
		{"pip install --system", "pip install --system requests", true},

		// Blocked — destructive operations
		{"rm -rf /", "rm -rf /", true},
		{"rm -rf town root", "rm -rf /home/ubuntu/gt", true},
		{"rm -rf home", "rm -rf ~", true},
		{"rm -rf town root glob", "rm -rf /home/ubuntu/gt/*", true},
		{"git push --force", "git push --force origin main", true},
		{"git reset --hard", "git reset --hard HEAD~1", true},
		{"git clean -f", "git clean -f", true},
		{"git clean -fd", "git clean -fd", true},
		{"drop table", "DROP TABLE users", true},

		// Allowed
		{"rm -rf ./build/", "rm -rf ./build/", false},
		{"rm -rf below town root", "rm -rf /home/ubuntu/gt/logs", false},
		{"rm -rf /tmp/cache/", "rm -rf /tmp/cache/", false},
		{"git push --force-with-lease", "git push --force-with-lease origin main", false},
		{"git push normal", "git push origin main", false},
		{"git reset soft", "git reset --soft HEAD~1", false},
		{"pip install (venv)", "pip install requests", false},
		{"npm install (local)", "npm install express", false},
		{"normal command", "ls -la", false},
	}
	withFixtureTownRoot(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lower := strings.ToLower(tt.command)
			blocked := false
			if matchesSudo(lower) != "" {
				blocked = true
			} else if matchesPackageInstall(lower) != "" {
				blocked = true
			} else if matchesDangerousRmRf(lower) != "" {
				blocked = true
			} else if matchesDangerousGitPush(lower) != "" {
				blocked = true
			} else {
				for _, p := range fragmentPatterns {
					if matchesAllFragments(lower, p.contains) {
						blocked = true
						break
					}
				}
			}
			if blocked != tt.blocked {
				t.Errorf("command %q: blocked=%v, want %v", tt.command, blocked, tt.blocked)
			}
		})
	}
}
