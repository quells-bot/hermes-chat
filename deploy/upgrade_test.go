package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type box struct {
	t      *testing.T
	dir    string
	script string
	stdout string // what the last run printed on stdout
}

// poll is the call the health loop makes after each failed check.
const poll = "is-failed --quiet hermes-chat"

func newBox(t *testing.T) *box {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH")
	}
	script, err := filepath.Abs("hermes-chat-upgrade")
	require.NoError(t, err)
	b := &box{t: t, dir: t.TempDir(), script: script}
	b.write("bin/systemctl", `#!/bin/sh
# Models a unit: "active" exists while the last start succeeded; when
# STUB_START_LIMIT is set, starting a BROKEN binary trips a start limit that
# refuses every start until a reset-failed call, and the unit reports failed
# until then. STUB_LOSE_PREVIOUS removes the kept binary during the rollback.
d="$(dirname "$0")/.."
echo "$*" >> "$d/calls.log"
case "$1" in
stop)
  rm -f "$d/active"
  if [ -n "$STUB_LOSE_PREVIOUS" ] && [ "$(grep -c '^stop' "$d/calls.log")" = 2 ]; then
    rm -f "$HERMES_CHAT_BIN.previous"
  fi
  if [ -n "$STUB_HUP_ON_SECOND_STOP" ] && [ "$(grep -c '^stop' "$d/calls.log")" = 2 ]; then
    kill -HUP "$PPID"
  fi
  ;;
reset-failed)
  rm -f "$d/limited"
  ;;
is-active)
  [ -f "$d/active" ]
  exit $?
  ;;
is-failed)
  [ -f "$d/limited" ]
  exit $?
  ;;
start)
  if [ -f "$d/limited" ] || grep -q STARTFAIL "$HERMES_CHAT_BIN" 2>/dev/null; then
    rm -f "$d/active"
    exit 1
  fi
  touch "$d/active"
  if grep -q BROKEN "$HERMES_CHAT_BIN" 2>/dev/null; then
    echo migrated >> "$STUB_DB"
    if [ -n "$STUB_START_LIMIT" ]; then
      touch "$d/limited"
    fi
  fi
  ;;
esac
exit 0
`)
	b.write("bin/curl", `#!/bin/sh
echo "$*" >> "$(dirname "$0")/../curl.log"
if [ -n "$STUB_CURL_SECONDS" ]; then
  sleep "$STUB_CURL_SECONDS"
fi
if [ -f "$HERMES_CHAT_BIN" ] && ! grep -q BROKEN "$HERMES_CHAT_BIN"; then
  exit 0
fi
echo "curl: (7) Failed to connect to chat.example.com port 443" >&2
exit 7
`)
	require.NoError(t, os.Chmod(b.path("bin/systemctl"), 0o755))
	require.NoError(t, os.Chmod(b.path("bin/curl"), 0o755))
	b.write("env", "HERMES_CHAT_DB_PATH="+b.path("state/chat.db")+"\nHERMES_CHAT_TLS_MODE=autocert\nHERMES_CHAT_TLS_DOMAIN=chat.example.com\nHERMES_CHAT_HTTP_ADDR=:443\n")
	return b
}

func (b *box) path(rel string) string { return filepath.Join(b.dir, rel) }

func (b *box) read(rel string) string {
	b.t.Helper()
	data, err := os.ReadFile(b.path(rel))
	require.NoError(b.t, err)
	return string(data)
}

func (b *box) write(rel, content string) {
	b.t.Helper()
	require.NoError(b.t, os.MkdirAll(filepath.Dir(b.path(rel)), 0o755))
	require.NoError(b.t, os.WriteFile(b.path(rel), []byte(content), 0o644))
}

func (b *box) list(rel string) []string {
	b.t.Helper()
	entries, err := os.ReadDir(b.path(rel))
	require.NoError(b.t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func (b *box) calls() []string {
	data, err := os.ReadFile(b.path("calls.log"))
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}

// steps is calls with each run of polls as one: how many times the health loop
// polls before its timeout depends on the clock.
func (b *box) steps() []string {
	var out []string
	for _, c := range b.calls() {
		if c == poll && len(out) > 0 && out[len(out)-1] == poll {
			continue
		}
		out = append(out, c)
	}
	return out
}

// lastLine is the last line of what a run printed on stderr.
func lastLine(stderr string) string {
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	return lines[len(lines)-1]
}

func (b *box) install(marker string) {
	b.t.Helper()
	b.write("installed/hermes-chat", "#!/bin/sh\n# "+marker+"\n")
	require.NoError(b.t, os.Chmod(b.path("installed/hermes-chat"), 0o755))
}

func (b *box) candidate(marker string) string {
	b.t.Helper()
	b.write("candidate", "#!/bin/sh\n# "+marker+"\necho 'usage: hermes-chat <command>' >&2\nexit 2\n")
	require.NoError(b.t, os.Chmod(b.path("candidate"), 0o755))
	return b.path("candidate")
}

func (b *box) run(candidate string) (int, string) {
	b.t.Helper()
	return b.runIn("", candidate, b.path("bin"))
}

func (b *box) runIn(dir, candidate, stubPath string, extraEnv ...string) (int, string) {
	b.t.Helper()
	cmd := exec.Command("bash", b.script, candidate)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"PATH="+stubPath+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HERMES_CHAT_ENV_FILE="+b.path("env"),
		"HERMES_CHAT_BIN="+b.path("installed/hermes-chat"),
		"HERMES_CHAT_UNIT=hermes-chat",
		"HERMES_CHAT_HEALTH_TIMEOUT=1",
		"STUB_DB="+b.path("state/chat.db"),
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	b.stdout = stdout.String()
	if err == nil {
		return 0, stderr.String()
	}
	var ee *exec.ExitError
	require.ErrorAs(b.t, err, &ee)
	return ee.ExitCode(), stderr.String()
}

func TestUpgrade_FirstInstall(t *testing.T) {
	b := newBox(t)
	code, _ := b.run(b.candidate("v1"))
	assert.Equal(t, 0, code)
	assert.Equal(t, "installed\n", b.stdout)
	assert.Contains(t, b.read("installed/hermes-chat"), "v1")
	assert.NoFileExists(t, b.path("installed/hermes-chat.previous"))
	assert.NoDirExists(t, b.path("state/backups"))
	assert.Equal(t, []string{"stop hermes-chat", "reset-failed hermes-chat", "start hermes-chat"}, b.calls())
}

func TestUpgrade_KeepsPreviousBinaryAndCopiesDatabase(t *testing.T) {
	b := newBox(t)
	b.install("v1")
	b.write("state/chat.db", "rows")
	b.write("state/chat.db-wal", "wal")
	code, _ := b.run(b.candidate("v2"))
	assert.Equal(t, 0, code)
	assert.Equal(t, "upgraded; previous binary kept at "+b.path("installed/hermes-chat.previous")+"\n", b.stdout)
	assert.Contains(t, b.read("installed/hermes-chat"), "v2")
	assert.Contains(t, b.read("installed/hermes-chat.previous"), "v1")
	stamps := b.list("state/backups")
	require.Len(t, stamps, 1)
	assert.Regexp(t, `^\d{8}T\d{6}Z$`, stamps[0])
	assert.Equal(t, "rows", b.read("state/backups/"+stamps[0]+"/chat.db"))
	assert.Equal(t, "wal", b.read("state/backups/"+stamps[0]+"/chat.db-wal"))
}

func TestUpgrade_RollsBackBinaryAndDatabase(t *testing.T) {
	b := newBox(t)
	b.install("v1")
	b.write("state/chat.db", "rows")
	code, stderr := b.run(b.candidate("BROKEN"))
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "rolled back to the previous version")
	assert.Contains(t, b.read("installed/hermes-chat"), "v1")
	assert.Equal(t, "rows", b.read("state/chat.db")) // the stub's "migrated" line is gone
	assert.Equal(t, []string{"stop hermes-chat", "reset-failed hermes-chat", "start hermes-chat", poll, "stop hermes-chat", "reset-failed hermes-chat", "start hermes-chat", "is-active --quiet hermes-chat"}, b.steps())
}

func TestUpgrade_FailedFirstInstallLeavesTheUnitStopped(t *testing.T) {
	b := newBox(t)
	code, stderr := b.run(b.candidate("BROKEN"))
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "nothing to roll back to")
	calls := b.calls()
	require.NotEmpty(t, calls)
	assert.Equal(t, "stop hermes-chat", calls[len(calls)-1])
}

func TestUpgrade_RejectsCandidateThatDoesNotRun(t *testing.T) {
	b := newBox(t)
	b.install("v1")
	for _, body := range []string{"not a program", "#!/bin/sh\nexit 0\n"} {
		p := b.path("upload")
		require.NoError(t, os.WriteFile(p, []byte(body), 0o755))
		code, stderr := b.run(p)
		assert.Equal(t, 1, code)
		assert.Contains(t, stderr, "candidate does not run on this host")
	}
	assert.Empty(t, b.calls()) // the running service was never stopped
	assert.Contains(t, b.read("installed/hermes-chat"), "v1")
}

func TestUpgrade_KeepsFiveBackups(t *testing.T) {
	b := newBox(t)
	b.install("v1")
	b.write("state/chat.db", "rows")
	for _, s := range []string{"20260101T000000Z", "20260102T000000Z", "20260103T000000Z", "20260104T000000Z", "20260105T000000Z", "20260106T000000Z"} {
		b.write("state/backups/"+s+"/chat.db", "old")
	}
	code, _ := b.run(b.candidate("v2"))
	assert.Equal(t, 0, code)
	stamps := b.list("state/backups")
	require.Len(t, stamps, 5)
	assert.NotContains(t, stamps, "20260101T000000Z")
	assert.NotContains(t, stamps, "20260102T000000Z")
}

func TestUpgrade_StopsNothingWithoutADatabasePath(t *testing.T) {
	b := newBox(t)
	b.write("env", "HERMES_CHAT_TLS_MODE=off\n")
	code, stderr := b.run(b.candidate("v1"))
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "HERMES_CHAT_DB_PATH")
	assert.Empty(t, b.calls())
}

func TestUpgrade_HealthURL(t *testing.T) {
	b := newBox(t)
	b.run(b.candidate("v1"))
	assert.Contains(t, b.read("curl.log"), "--resolve chat.example.com:443:127.0.0.1 https://chat.example.com:443/healthz")

	b = newBox(t)
	b.write("env", "HERMES_CHAT_DB_PATH="+b.path("state/chat.db")+"\nHERMES_CHAT_TLS_MODE=off\nHERMES_CHAT_HTTP_ADDR=127.0.0.1:8080\n")
	b.run(b.candidate("v1"))
	assert.Contains(t, b.read("curl.log"), "http://127.0.0.1:8080/healthz")
	assert.NotContains(t, b.read("curl.log"), "--resolve")
}

func TestUpgrade_Usage(t *testing.T) {
	b := newBox(t)
	out, err := exec.Command("bash", b.script).CombinedOutput()
	var ee *exec.ExitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 2, ee.ExitCode())
	assert.Contains(t, string(out), "usage: hermes-chat-upgrade CANDIDATE")
}

func TestUpgrade_RollsBackWhenTheServiceWillNotStart(t *testing.T) {
	b := newBox(t)
	b.install("v1")
	b.write("state/chat.db", "rows")
	code, stderr := b.run(b.candidate("STARTFAIL"))
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "rolled back to the previous version")
	assert.Contains(t, b.read("installed/hermes-chat"), "v1")
	assert.Equal(t, "rows", b.read("state/chat.db"))
	assert.Equal(t, []string{"stop hermes-chat", "reset-failed hermes-chat", "start hermes-chat", "stop hermes-chat", "reset-failed hermes-chat", "start hermes-chat", "is-active --quiet hermes-chat"}, b.calls())
}

func TestUpgrade_RestartsTheOldVersionWhenSetupFailsAfterStop(t *testing.T) {
	b := newBox(t)
	b.install("v1")
	b.write("state/chat.db", "rows")
	b.write("state/backups", "in the way") // mkdir of the backup directory fails
	code, stderr := b.run(b.candidate("v2"))
	assert.NotEqual(t, 0, code)
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	assert.Equal(t, "upgrade failed; restarted the previous version", lines[len(lines)-1])
	assert.Contains(t, stderr, "restarted the previous version")
	assert.Contains(t, b.read("installed/hermes-chat"), "v1")
	assert.Equal(t, []string{"stop hermes-chat", "reset-failed hermes-chat", "start hermes-chat", "is-active --quiet hermes-chat"}, b.calls())
}

func TestUpgrade_BareNameCandidateIsTheFileInTheWorkingDirectory(t *testing.T) {
	b := newBox(t)
	b.install("v1")
	b.write("work/hermes-chat", "not a program")
	require.NoError(t, os.Chmod(b.path("work/hermes-chat"), 0o755))
	good := b.candidate("good")
	require.NoError(t, os.MkdirAll(b.path("onpath"), 0o755))
	require.NoError(t, os.Rename(good, b.path("onpath/hermes-chat")))
	code, stderr := b.runIn(b.path("work"), "hermes-chat", b.path("bin")+string(os.PathListSeparator)+b.path("onpath"))
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "candidate does not run on this host")
	assert.Empty(t, b.calls())
	assert.Contains(t, b.read("installed/hermes-chat"), "v1")
}

func TestUpgrade_RecoveryFinishesWhenSignalledDuringRollback(t *testing.T) {
	b := newBox(t)
	b.install("v1")
	b.write("state/chat.db", "rows")
	// The rollback's stop (the second one) sends the script a HUP before returning.
	code, stderr := b.runIn("", b.candidate("BROKEN"), b.path("bin"), "STUB_HUP_ON_SECOND_STOP=1")
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "rolled back to the previous version")
	assert.Contains(t, b.read("installed/hermes-chat"), "v1")
	assert.Equal(t, "rows", b.read("state/chat.db"))
	assert.Equal(t, []string{"stop hermes-chat", "reset-failed hermes-chat", "start hermes-chat", poll, "stop hermes-chat", "reset-failed hermes-chat", "start hermes-chat", "is-active --quiet hermes-chat"}, b.steps())
}

func TestUpgrade_RollbackClearsTheStartLimit(t *testing.T) {
	b := newBox(t)
	b.install("v1")
	b.write("state/chat.db", "rows")
	code, stderr := b.runIn("", b.candidate("BROKEN"), b.path("bin"), "STUB_START_LIMIT=1")
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "new version failed its health check; rolled back to the previous version")
	assert.NotContains(t, stderr, "did not start")
	assert.Contains(t, b.read("installed/hermes-chat"), "v1")
	assert.FileExists(t, b.path("active")) // the old version is running again
	assert.NoFileExists(t, b.path("limited"))
}

func TestUpgrade_ReportsARollbackThatDidNotStart(t *testing.T) {
	b := newBox(t)
	b.install("v1 STARTFAIL") // the previous version cannot start either
	b.write("state/chat.db", "rows")
	code, stderr := b.run(b.candidate("BROKEN"))
	assert.Equal(t, 1, code)
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	assert.Equal(t, "new version failed its health check; rolled back to the previous version, but it did not start: check systemctl status hermes-chat", lines[len(lines)-1])
	assert.Contains(t, b.read("installed/hermes-chat"), "v1")
}

func TestUpgrade_ReportsARestartThatDidNotStart(t *testing.T) {
	b := newBox(t)
	b.install("v1 STARTFAIL")
	b.write("state/backups", "in the way") // fails after the stop, before the swap
	b.write("state/chat.db", "rows")
	code, stderr := b.run(b.candidate("v2"))
	assert.Equal(t, 1, code)
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	assert.Equal(t, "upgrade failed; the previous version did not start: check systemctl status hermes-chat", lines[len(lines)-1])
}

func TestUpgrade_PrintsWhyTheHealthCheckFailed(t *testing.T) {
	b := newBox(t)
	b.install("v1")
	b.write("state/chat.db", "rows")
	code, stderr := b.run(b.candidate("BROKEN"))
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "last health check error: curl: (7) Failed to connect to chat.example.com port 443\n")
	assert.Equal(t, "new version failed its health check; rolled back to the previous version", lastLine(stderr))
}

func TestUpgrade_StopsNothingWithoutCurlOrSystemctl(t *testing.T) {
	for _, missing := range []string{"curl", "systemctl"} {
		b := newBox(t)
		b.install("v1")
		require.NoError(t, os.Remove(b.path("bin/"+missing)))
		// PATH holds the stubs only, so the host's own curl or systemctl is not found.
		code, stderr := b.runIn("", b.candidate("v2"), b.path("bin"), "PATH="+b.path("bin"))
		assert.Equal(t, 1, code, missing)
		assert.Equal(t, missing+" not found on PATH\n", stderr, missing)
		assert.Empty(t, b.calls(), missing)
		assert.Contains(t, b.read("installed/hermes-chat"), "v1", missing)
	}
}

func TestUpgrade_StopsNothingWithABadHealthTimeout(t *testing.T) {
	for _, bad := range []string{"0", "-1", "abc", "1.5", "5s", " 5"} {
		b := newBox(t)
		b.install("v1")
		code, stderr := b.runIn("", b.candidate("v2"), b.path("bin"), "HERMES_CHAT_HEALTH_TIMEOUT="+bad)
		assert.Equal(t, 1, code, bad)
		assert.Equal(t, "HERMES_CHAT_HEALTH_TIMEOUT must be a positive integer\n", stderr, bad)
		assert.Empty(t, b.calls(), bad)
		assert.Contains(t, b.read("installed/hermes-chat"), "v1", bad)
	}
}

// The timeout is seconds on the clock, not a number of checks: one check that
// takes the whole timeout is the only one.
func TestUpgrade_HealthTimeoutIsWallClock(t *testing.T) {
	b := newBox(t)
	b.install("v1")
	b.write("state/chat.db", "rows")
	code, stderr := b.runIn("", b.candidate("BROKEN"), b.path("bin"), "HERMES_CHAT_HEALTH_TIMEOUT=2", "STUB_CURL_SECONDS=2")
	assert.Equal(t, 1, code)
	assert.Equal(t, "new version failed its health check; rolled back to the previous version", lastLine(stderr))
	assert.Equal(t, 1, strings.Count(b.read("curl.log"), "\n"))
}

// A unit that has hit its start limit will not come up: the script does not
// wait out the timeout.
func TestUpgrade_StopsWaitingWhenTheUnitGivesUp(t *testing.T) {
	b := newBox(t)
	b.install("v1")
	b.write("state/chat.db", "rows")
	code, stderr := b.runIn("", b.candidate("BROKEN"), b.path("bin"), "STUB_START_LIMIT=1", "HERMES_CHAT_HEALTH_TIMEOUT=10")
	assert.Equal(t, 1, code)
	assert.Equal(t, "new version failed its health check; rolled back to the previous version", lastLine(stderr))
	assert.Equal(t, []string{"stop hermes-chat", "reset-failed hermes-chat", "start hermes-chat", poll, "stop hermes-chat", "reset-failed hermes-chat", "start hermes-chat", "is-active --quiet hermes-chat"}, b.calls())
	assert.Equal(t, 1, strings.Count(b.read("curl.log"), "\n"))
}

func TestUpgrade_ReportsARestoreThatFailed(t *testing.T) {
	b := newBox(t)
	b.install("v1")
	b.write("state/chat.db", "rows")
	// The kept binary disappears before the rollback can move it back.
	code, stderr := b.runIn("", b.candidate("BROKEN"), b.path("bin"), "STUB_LOSE_PREVIOUS=1")
	assert.Equal(t, 1, code)
	stamps := b.list("state/backups")
	require.Len(t, stamps, 1)
	assert.Equal(t, "new version failed its health check; could not restore the previous version: check "+b.path("installed/hermes-chat.previous")+" and "+b.path("state/backups/"+stamps[0]), lastLine(stderr))
	assert.NotContains(t, stderr, "rolled back")
	// Nothing is started on a host that was not put back.
	assert.Equal(t, []string{"stop hermes-chat", "reset-failed hermes-chat", "start hermes-chat", poll, "stop hermes-chat"}, b.steps())
}
