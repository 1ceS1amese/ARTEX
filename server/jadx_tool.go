package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
)

const (
	jadxDefaultTimeout = 180
	jadxMaxInputBytes  = 512 << 20
)

type jadxInput struct {
	InputPath      string `json:"input_path"`
	Mode           string `json:"mode"`
	Threads        int    `json:"threads"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

type jadxOutput struct {
	Status        string `json:"status"`
	OutputDir     string `json:"output_dir,omitempty"`
	JavaFiles     int    `json:"java_files"`
	ResourceFiles int    `json:"resource_files"`
	ExitCode      int    `json:"exit_code"`
	DurationMS    int64  `json:"duration_ms"`
	Message       string `json:"message,omitempty"`
}

func newJadxTool() actool.CoreTool {
	return actool.Build(actool.Spec{
		Name:        "jadx_decompile",
		Description: "使用内置 JADX 反编译当前会话工作目录内的 Android/Java 文件，返回 Java 源码与资源目录；可通过 ARTEX_JADX_BIN 覆盖程序路径。",
		Schema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"input_path":      map[string]any{"type": "string", "description": "工作目录内的 APK/DEX/JAR 等文件路径"},
				"mode":            map[string]any{"type": "string", "enum": []string{"auto", "restructure", "simple", "fallback"}, "default": "auto"},
				"threads":         map[string]any{"type": "integer", "minimum": 1, "maximum": 8, "default": 2},
				"timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 600, "default": jadxDefaultTimeout},
			},
			"required": []string{"input_path"},
		},
		Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
			return permission.AskUser("运行 JADX 反编译并在当前工作目录写入源码文件？")
		},
		Run: runJadx,
	})
}

func runJadx(ctx context.Context, raw json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
	if tc == nil || tc.WorkingDir == "" {
		return actool.Errorf("JADX 工具缺少会话工作目录"), nil
	}
	var in jadxInput
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return actool.Errorf("JADX 参数无效: " + err.Error()), nil
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return actool.Errorf("JADX 参数必须是单个 JSON 对象"), nil
	}
	if in.Mode == "" {
		in.Mode = "auto"
	}
	if in.Threads == 0 {
		in.Threads = 2
	}
	if in.TimeoutSeconds == 0 {
		in.TimeoutSeconds = jadxDefaultTimeout
	}
	if in.Mode != "auto" && in.Mode != "restructure" && in.Mode != "simple" && in.Mode != "fallback" {
		return actool.Errorf("JADX mode 不支持"), nil
	}
	if in.Threads < 1 || in.Threads > 8 || in.TimeoutSeconds < 1 || in.TimeoutSeconds > 600 {
		return actool.Errorf("JADX threads 需为 1–8，timeout_seconds 需为 1–600"), nil
	}
	root, err := filepath.EvalSymlinks(tc.WorkingDir)
	if err != nil {
		return actool.Errorf("无法访问会话工作目录: " + err.Error()), nil
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return actool.Errorf("无法解析会话工作目录"), nil
	}
	input, err := jadxInputFile(root, in.InputPath)
	if err != nil {
		return actool.Errorf(err.Error()), nil
	}
	bin, prefix, err := jadxCommand()
	if err != nil {
		return actool.Errorf(err.Error()), nil
	}
	outputParent := filepath.Join(root, "jadx-output")
	if err := os.MkdirAll(outputParent, 0o700); err != nil {
		return actool.Errorf("无法创建 JADX 输出目录: " + err.Error()), nil
	}
	outputParent, err = filepath.EvalSymlinks(outputParent)
	if err != nil || !jadxWithin(root, outputParent) {
		return actool.Errorf("JADX 输出目录不在会话工作目录内"), nil
	}
	output, err := os.MkdirTemp(outputParent, "run-")
	if err != nil {
		return actool.Errorf("无法创建 JADX 输出目录: " + err.Error()), nil
	}
	limitCtx, cancel := context.WithTimeout(ctx, time.Duration(in.TimeoutSeconds)*time.Second)
	defer cancel()
	args := append(prefix, "-d", output, "-j", strconv.Itoa(in.Threads), "-m", in.Mode,
		"--config", "none", "--log-level", "warn", input)
	cmd := exec.CommandContext(limitCtx, bin, args...)
	cmd.Dir = root
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = append(os.Environ(), tc.Env...)
	// Never inherit flags that disable JADX's ZIP/XML safety checks from the host.
	cmd.Env = append(cmd.Env, "JADX_DISABLE_XML_SECURITY=false", "JADX_DISABLE_ZIP_SECURITY=false", "JADX_DISABLE_ALL_SECURITY_FLAGS=false")
	// JADX output can include content from untrusted files. Do not persist it in
	// the agent transcript; return bounded structural diagnostics instead.
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	started := time.Now()
	runErr := cmd.Run()
	javaFiles, resourceFiles := jadxCountFiles(output)
	relOutput, _ := filepath.Rel(root, output)
	result := jadxOutput{
		Status: "ok", OutputDir: filepath.ToSlash(relOutput), JavaFiles: javaFiles,
		ResourceFiles: resourceFiles, ExitCode: 0, DurationMS: time.Since(started).Milliseconds(),
	}
	if runErr != nil {
		result.Status = "error"
		result.Message = "JADX 执行失败"
		if errors.Is(limitCtx.Err(), context.DeadlineExceeded) {
			result.Message = "JADX 执行超时"
		}
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = -1
		}
		if javaFiles+resourceFiles > 0 {
			result.Status = "partial"
			result.Message += "，已保留部分输出"
		}
	} else if javaFiles+resourceFiles == 0 {
		result.Status = "error"
		result.Message = "JADX 未生成任何输出文件"
	}
	b, err := json.Marshal(result)
	if err != nil {
		return actool.Errorf("JADX 结果编码失败"), nil
	}
	res := actool.Text(string(b))
	res.IsError = result.Status == "error"
	return res, nil
}

func jadxInputFile(root, requested string) (string, error) {
	if strings.TrimSpace(requested) == "" {
		return "", errors.New("JADX input_path 不能为空")
	}
	path := requested
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path, err := filepath.EvalSymlinks(path)
	if err != nil || !jadxWithin(root, path) {
		return "", errors.New("JADX 输入文件不存在或不在会话工作目录内")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("JADX 输入必须是普通文件")
	}
	if info.Size() > jadxMaxInputBytes {
		return "", fmt.Errorf("JADX 输入超过 %d MiB 限制", jadxMaxInputBytes>>20)
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".apk", ".dex", ".jar", ".class", ".smali", ".zip", ".aar", ".arsc", ".aab", ".xapk", ".apkm":
		return path, nil
	default:
		return "", errors.New("JADX 不支持此文件扩展名")
	}
}

func jadxWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func jadxCommand() (string, []string, error) {
	configured := strings.TrimSpace(os.Getenv("ARTEX_JADX_BIN"))
	if configured == "" {
		bin, err := exec.LookPath("jadx")
		if err != nil {
			return "", nil, errors.New("未找到 JADX；请设置 ARTEX_JADX_BIN 或将 jadx 加入 PATH")
		}
		if ext := strings.ToLower(filepath.Ext(bin)); ext == ".bat" || ext == ".cmd" {
			return "", nil, errors.New("PATH 中的 JADX 是批处理脚本；请将 ARTEX_JADX_BIN 指向官方 all.jar")
		}
		return bin, nil, nil
	}
	if strings.EqualFold(filepath.Ext(configured), ".jar") {
		if info, err := os.Stat(configured); err != nil || !info.Mode().IsRegular() {
			return "", nil, errors.New("ARTEX_JADX_BIN 指向的 JAR 不存在")
		}
		java, err := exec.LookPath("java")
		if err != nil {
			return "", nil, errors.New("运行 JADX JAR 需要 java 位于 PATH")
		}
		// The all-JAR manifest starts JadxGUI, which can exit 0 in headless
		// environments without decompiling anything. Select the CLI explicitly.
		return java, []string{"-cp", configured, "jadx.cli.JadxCLI"}, nil
	}
	if ext := strings.ToLower(filepath.Ext(configured)); ext == ".bat" || ext == ".cmd" {
		return "", nil, errors.New("请将 ARTEX_JADX_BIN 指向 JADX 可执行文件或 all.jar；不直接执行批处理脚本")
	}
	bin, err := exec.LookPath(configured)
	if err != nil {
		return "", nil, errors.New("ARTEX_JADX_BIN 指向的 JADX 程序不可执行")
	}
	return bin, nil, nil
}

func jadxCountFiles(root string) (javaFiles, resourceFiles int) {
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".java") {
			javaFiles++
		} else {
			resourceFiles++
		}
		return nil
	})
	return
}
