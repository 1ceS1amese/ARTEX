package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	actool "github.com/Autumn-27/norma/tool"
)

func TestJadxInputStaysInWorkdir(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "sample.apk")
	if err := os.WriteFile(inside, []byte("apk"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := jadxInputFile(root, "sample.apk"); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside.apk")
	if err := os.WriteFile(outside, []byte("apk"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{outside, filepath.Join("..", filepath.Base(filepath.Dir(outside)), "outside.apk")} {
		if _, err := jadxInputFile(root, input); err == nil {
			t.Errorf("outside input accepted: %s", input)
		}
	}
	link := filepath.Join(root, "linked.apk")
	if err := os.Symlink(outside, link); err == nil {
		if _, err := jadxInputFile(root, link); err == nil {
			t.Error("symlink escape accepted")
		}
	}
	if err := os.WriteFile(filepath.Join(root, "script.jadx.kts"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := jadxInputFile(root, "script.jadx.kts"); err == nil {
		t.Error("JADX script accepted as data input")
	}
}

func TestJadxToolExecutesWithoutShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test executable is a POSIX shell script")
	}
	root := t.TempDir()
	inputName := "sample;touch SHOULD_NOT_EXIST.apk"
	if err := os.WriteFile(filepath.Join(root, inputName), []byte("apk"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "fake-jadx")
	script := `#!/bin/sh
set -eu
out=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-d" ]; then out="$2"; shift 2; else shift; fi
done
mkdir -p "$out/sources"
printf 'class Test {}\n' > "$out/sources/Test.java"
printf '%s\n' "$JADX_DISABLE_XML_SECURITY:$JADX_DISABLE_ZIP_SECURITY:$JADX_DISABLE_ALL_SECURITY_FLAGS" > "$out/security.txt"
`
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ARTEX_JADX_BIN", bin)
	t.Setenv("JADX_DISABLE_XML_SECURITY", "true")
	res, err := newJadxTool().Call(context.Background(), json.RawMessage(`{"input_path":"`+inputName+`"}`), &actool.ToolContext{WorkingDir: root})
	if err != nil || res.IsError {
		t.Fatalf("JADX call failed: %v %s", err, res.Flatten())
	}
	var out jadxOutput
	if err := json.Unmarshal([]byte(res.Flatten()), &out); err != nil {
		t.Fatal(err)
	}
	if out.Status != "ok" || out.JavaFiles != 1 || out.ResourceFiles != 1 {
		t.Fatalf("unexpected output: %+v", out)
	}
	if _, err := os.Stat(filepath.Join(root, "SHOULD_NOT_EXIST.apk")); err == nil {
		t.Fatal("input was interpreted by a shell")
	}
	security, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(out.OutputDir), "security.txt"))
	if err != nil || strings.TrimSpace(string(security)) != "false:false:false" {
		t.Fatalf("JADX security flags: %q, %v", security, err)
	}
}

func TestJadxRejectsOutputSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.apk"), []byte("apk"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "jadx-output")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	bin := filepath.Join(t.TempDir(), "jadx")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ARTEX_JADX_BIN", bin)
	res, err := newJadxTool().Call(context.Background(), json.RawMessage(`{"input_path":"sample.apk"}`), &actool.ToolContext{WorkingDir: root})
	if err != nil || !res.IsError || !strings.Contains(res.Flatten(), "输出目录不在") {
		t.Fatalf("output escape not rejected: %v %s", err, res.Flatten())
	}
}

func TestJadxTimeoutStopsProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test executable is a POSIX shell script")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.apk"), []byte("apk"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "slow-jadx")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexec sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ARTEX_JADX_BIN", bin)
	res, err := newJadxTool().Call(context.Background(), json.RawMessage(`{"input_path":"sample.apk","timeout_seconds":1}`), &actool.ToolContext{WorkingDir: root})
	if err != nil || !res.IsError {
		t.Fatalf("timeout was not reported: %v %s", err, res.Flatten())
	}
	var out jadxOutput
	if err := json.Unmarshal([]byte(res.Flatten()), &out); err != nil {
		t.Fatal(err)
	}
	if out.Status != "error" || !strings.Contains(out.Message, "超时") {
		t.Fatalf("unexpected timeout result: %+v", out)
	}
}

func TestJadxRejectsSuccessfulExitWithoutFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test executable is a POSIX shell script")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.apk"), []byte("apk"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "empty-jadx")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ARTEX_JADX_BIN", bin)
	res, err := newJadxTool().Call(context.Background(), json.RawMessage(`{"input_path":"sample.apk"}`), &actool.ToolContext{WorkingDir: root})
	if err != nil || !res.IsError || !strings.Contains(res.Flatten(), "未生成任何输出文件") {
		t.Fatalf("empty output was accepted: %v %s", err, res.Flatten())
	}
}

func TestJadxRealAPK(t *testing.T) {
	bin, apk := os.Getenv("ARTEX_JADX_TEST_BIN"), os.Getenv("ARTEX_JADX_TEST_APK")
	if bin == "" || apk == "" {
		t.Skip("set ARTEX_JADX_TEST_BIN and ARTEX_JADX_TEST_APK for integration test")
	}
	data, err := os.ReadFile(apk)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	inputName := "sample;touch SHOULD_NOT_EXIST.apk"
	if err := os.WriteFile(filepath.Join(root, inputName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ARTEX_JADX_BIN", bin)
	res, err := newJadxTool().Call(context.Background(), json.RawMessage(`{"input_path":"`+inputName+`","threads":2,"timeout_seconds":120}`), &actool.ToolContext{WorkingDir: root})
	if err != nil || res.IsError {
		t.Fatalf("real JADX failed: %v %s", err, res.Flatten())
	}
	var out jadxOutput
	if err := json.Unmarshal([]byte(res.Flatten()), &out); err != nil {
		t.Fatal(err)
	}
	if out.Status != "ok" || out.JavaFiles == 0 {
		t.Fatalf("no Java source produced: %+v", out)
	}
	if _, err := os.Stat(filepath.Join(root, "SHOULD_NOT_EXIST.apk")); err == nil {
		t.Fatal("input was interpreted as a command")
	}
}
