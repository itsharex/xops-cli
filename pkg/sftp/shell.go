package sftp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/chzyer/readline"
	"github.com/schollz/progressbar/v3"
	"github.com/wentf9/xops-cli/pkg/i18n"
	"golang.org/x/term"
)

// Shell 定义交互式 SFTP 环境
type Shell struct {
	client   *Client
	cwd      string // 远程当前目录
	localCwd string // 本地当前目录
	rl       *readline.Instance
	stderr   io.Writer
}

// NewShell 创建一个新的交互式 Shell
func (c *Client) NewShell(stdin io.Reader, stdout, stderr io.Writer) (*Shell, error) {
	// 获取初始远程目录
	cwd, err := c.sftpClient.Getwd()
	if err != nil {
		cwd = "."
	}

	// 获取初始本地目录
	localCwd, err := os.Getwd()
	if err != nil {
		localCwd = "."
	}

	// 创建 readline 实例
	rl, err := readline.NewEx(&readline.Config{
		Prompt:          fmt.Sprintf("sftp:%s> ", cwd),
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
		HistoryFile:     "",  // 不持久化历史，可根据需要设置路径
		AutoComplete:    nil, // 先设为 nil，创建 shell 后再绑定
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create readline: %w", err)
	}

	shell := &Shell{
		client:   c,
		cwd:      cwd,
		localCwd: localCwd,
		rl:       rl,
		stderr:   stderr,
	}

	// 绑定自动补全器
	rl.Config.AutoComplete = &SftpCompleter{shell: shell}

	return shell, nil
}

// Run 启动交互式循环 (REPL)
func (s *Shell) Run(ctx context.Context) error {
	defer func() { _ = s.rl.Close() }()

	for {
		// 更新 prompt 显示当前远程目录
		s.rl.SetPrompt(fmt.Sprintf("sftp:%s> ", s.cwd))

		line, err := s.rl.Readline()
		if err != nil {
			// readline.ErrInterrupt 表示 Ctrl+C
			// io.EOF 表示 Ctrl+D
			if errors.Is(err, readline.ErrInterrupt) {
				continue // 忽略中断，继续等待输入
			}
			return nil // EOF 或其他错误，退出
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		args := strings.Fields(line)
		cmd := args[0]
		params := args[1:]

		exit, err := s.dispatchCommand(ctx, cmd, params)
		if exit {
			return err
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
}

func (s *Shell) dispatchCommand(ctx context.Context, cmd string, params []string) (bool, error) {
	switch cmd {
	case "exit", "quit", "bye":
		return true, nil
	case "help", "?":
		s.printHelp()
	case "pwd":
		_, _ = fmt.Fprintln(s.rl.Stdout(), s.cwd)
	case "lpwd":
		_, _ = fmt.Fprintln(s.rl.Stdout(), s.localCwd)
	case "ls":
		s.handleLs(params, false)
	case "ll":
		s.handleLs(params, true)
	case "lls":
		s.handleLocalLs(params, false)
	case "lll":
		s.handleLocalLs(params, true)
	case "cd":
		s.handleCd(params)
	case "lcd":
		s.handleLocalCd(params)
	case "mkdir":
		s.handleMkdir(params)
	case "rm":
		s.handleRm(params)
	case "get":
		s.handleGet(ctx, params)
	case "put":
		s.handlePut(ctx, params)
	default:
		_, _ = fmt.Fprintf(s.stderr, "%s\n", i18n.Tf("sftp_shell_unknown_cmd", map[string]any{"Cmd": cmd}))
	}
	return false, nil
}

// ================= 命令处理逻辑 =================

func (s *Shell) resolvePath(p string) string {
	// SFTP 协议强制使用 / 作为路径分隔符
	// 使用 strings.HasPrefix 判断绝对路径，而非 filepath.IsAbs
	// 因为 filepath.IsAbs 依赖本地操作系统规则（Windows 会认为 /home 是相对路径）
	if strings.HasPrefix(p, "/") {
		return p
	}
	return s.client.JoinPath(s.cwd, p)
}

func (s *Shell) resolveLocalPath(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(s.localCwd, p)
}

func (s *Shell) handleCd(args []string) {
	if len(args) == 0 {
		return
	}
	target := s.resolvePath(args[0])

	// 检查目录是否存在
	info, err := s.client.sftpClient.Stat(target)
	if err != nil {
		_, _ = fmt.Fprintf(s.stderr, "cd: %v\n", err)
		return
	}
	if !info.IsDir() {
		_, _ = fmt.Fprintf(s.stderr, "%s\n", i18n.Tf("sftp_shell_cd_not_dir", map[string]any{"Path": args[0]}))
		return
	}
	s.cwd = target
}

func (s *Shell) handleLocalCd(args []string) {
	if len(args) == 0 {
		return
	}
	target := s.resolveLocalPath(args[0])
	if err := os.Chdir(target); err != nil {
		_, _ = fmt.Fprintf(s.stderr, "lcd: %v\n", err)
		return
	}
	// 更新本地当前目录
	s.localCwd, _ = os.Getwd()
}

func (s *Shell) handleLs(args []string, long bool) {
	path := s.cwd
	if len(args) > 0 {
		path = s.resolvePath(args[0])
	}

	files, err := s.client.sftpClient.ReadDir(path)
	if err != nil {
		_, _ = fmt.Fprintf(s.stderr, "ls: %v\n", err)
		return
	}

	if long {
		// 详细列表模式 (类似 ls -l)
		w := tabwriter.NewWriter(s.rl.Stdout(), 0, 0, 1, ' ', 0)
		for _, f := range files {
			modTime := f.ModTime().Format("Jan 02 15:04")
			size := formatBytes(f.Size())
			name := f.Name()
			if f.IsDir() {
				name += "/"
			}
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", f.Mode(), size, modTime, name)
		}
		_ = w.Flush()
	} else {
		// 简单列表模式 (多列输出)
		names := make([]string, 0, len(files))
		for _, f := range files {
			name := f.Name()
			if f.IsDir() {
				name += "/"
			}
			names = append(names, name)
		}
		s.printColumns(names)
	}
}

func (s *Shell) handleLocalLs(args []string, long bool) {
	path := s.localCwd
	if len(args) > 0 {
		path = s.resolveLocalPath(args[0])
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		_, _ = fmt.Fprintf(s.stderr, "lls: %v\n", err)
		return
	}

	if long {
		// 详细列表模式
		w := tabwriter.NewWriter(s.rl.Stdout(), 0, 0, 1, ' ', 0)
		for _, e := range entries {
			info, err := e.Info()
			if err != nil {
				continue
			}
			modTime := info.ModTime().Format("Jan 02 15:04")
			size := formatBytes(info.Size())
			name := e.Name()
			if e.IsDir() {
				name += string(filepath.Separator)
			}
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", info.Mode(), size, modTime, name)
		}
		_ = w.Flush()
	} else {
		// 简单列表模式 (多列输出)
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() {
				name += string(filepath.Separator)
			}
			names = append(names, name)
		}
		s.printColumns(names)
	}
}

func (s *Shell) handleGet(ctx context.Context, args []string) {
	if len(args) < 1 {
		_, _ = fmt.Fprintln(s.stderr, i18n.T("sftp_shell_get_usage"))
		return
	}
	remote := s.resolvePath(args[0])
	local := filepath.Base(remote)
	if len(args) > 1 {
		local = s.resolveLocalPath(args[1])
	}

	// 检查目标文件是否已存在
	if lStat, err := os.Stat(local); err == nil {
		localDest := local
		if lStat.IsDir() {
			localDest = filepath.Join(local, filepath.Base(remote))
		}
		if _, err := os.Stat(localDest); err == nil {
			if s.client.config.NoOverwrite {
				return
			}
			if !s.client.config.Force {
				if !s.askConfirmation(i18n.Tf("prompt_overwrite", map[string]any{"Path": localDest})) {
					return
				}
				origForce := s.client.config.Force
				s.client.SetForce(true)
				defer s.client.SetForce(origForce)
			}
		}
	}

	_, _ = fmt.Fprintln(s.rl.Stdout(), i18n.Tf("sftp_shell_downloading", map[string]any{"Remote": remote, "Local": local}))

	progress := s.createProgressBar(remote)

	err := s.client.Download(ctx, remote, local, progress)
	if err != nil {
		_, _ = fmt.Fprintf(s.stderr, "%s\n", i18n.Tf("sftp_shell_download_failed", map[string]any{"Error": err}))
	} else {
		_, _ = fmt.Fprintln(s.rl.Stdout(), i18n.T("sftp_shell_download_done"))
	}
}

func (s *Shell) handlePut(ctx context.Context, args []string) {
	if len(args) < 1 {
		_, _ = fmt.Fprintln(s.stderr, i18n.T("sftp_shell_put_usage"))
		return
	}
	local := s.resolveLocalPath(args[0])
	var remote string

	if len(args) > 1 {
		remote = s.resolvePath(args[1])
	} else {
		remote = s.client.JoinPath(s.cwd, filepath.Base(local))
	}

	// 检查目标文件是否已存在
	remoteStat, err := s.client.sftpClient.Stat(remote)
	if err == nil && remoteStat.IsDir() {
		remote = s.client.JoinPath(remote, filepath.Base(local))
	}
	if _, err := s.client.sftpClient.Stat(remote); err == nil {
		if s.client.config.NoOverwrite {
			return
		}
		if !s.client.config.Force {
			if !s.askConfirmation(i18n.Tf("prompt_overwrite", map[string]any{"Path": remote})) {
				return
			}
			origForce := s.client.config.Force
			s.client.SetForce(true)
			defer s.client.SetForce(origForce)
		}
	}

	_, _ = fmt.Fprintln(s.rl.Stdout(), i18n.Tf("sftp_shell_uploading", map[string]any{"Local": local, "Remote": remote}))

	// 计算本地文件大小以显示准确的进度条
	var totalSize int64
	_ = filepath.Walk(local, func(_ string, info os.FileInfo, _ error) error {
		if !info.IsDir() {
			totalSize += info.Size()
		}
		return nil
	})

	bar := progressbar.NewOptions64(
		totalSize,
		progressbar.OptionSetDescription("Uploading"),
		progressbar.OptionSetWriter(s.rl.Stdout()), // 关键：使用 readline 的 stdout
		progressbar.OptionEnableColorCodes(true),
		progressbar.OptionShowBytes(true),
		progressbar.OptionSetWidth(30),
		progressbar.OptionThrottle(65*time.Millisecond),
		progressbar.OptionOnCompletion(func() {
			_, _ = fmt.Fprint(s.rl.Stdout(), "\n")
		}),
	)
	callback := func(n int64) { _ = bar.Add64(n) }

	err = s.client.Upload(ctx, local, remote, callback)
	if err != nil {
		_, _ = fmt.Fprintf(s.stderr, "%s\n", i18n.Tf("sftp_shell_upload_failed", map[string]any{"Error": err}))
	} else {
		_, _ = fmt.Fprintln(s.rl.Stdout(), i18n.T("sftp_shell_upload_done"))
	}
}

func (s *Shell) handleMkdir(args []string) {
	if len(args) < 1 {
		_, _ = fmt.Fprintln(s.stderr, i18n.T("sftp_shell_mkdir_usage"))
		return
	}
	path := s.resolvePath(args[0])
	if err := s.client.sftpClient.Mkdir(path); err != nil {
		_, _ = fmt.Fprintf(s.stderr, "mkdir: %v\n", err)
	}
}

func (s *Shell) handleRm(args []string) {
	if len(args) < 1 {
		_, _ = fmt.Fprintln(s.stderr, i18n.T("sftp_shell_rm_usage"))
		return
	}
	path := s.resolvePath(args[0])
	if err := s.client.sftpClient.Remove(path); err != nil {
		// 尝试作为目录删除
		if err2 := s.client.sftpClient.RemoveDirectory(path); err2 != nil {
			_, _ = fmt.Fprintf(s.stderr, "rm: %v\n", err)
		}
	}
}

func (s *Shell) printHelp() {
	_, _ = fmt.Fprintln(s.rl.Stdout(), i18n.T("sftp_shell_help"))
}

func (s *Shell) askConfirmation(prompt string) bool {
	// 关键：在询问前显式换行并刷新，防止提示被上一次命令的输出覆盖或吞掉
	_, _ = fmt.Fprint(s.rl.Stdout(), "\n")

	// 设置一个新的临时 prompt，这样 Readline 会自动渲染它并等待输入
	origPrompt := s.rl.Config.Prompt
	defer s.rl.SetPrompt(origPrompt)

	s.rl.SetPrompt(fmt.Sprintf("%s [y/N]: ", prompt))
	line, err := s.rl.Readline()
	if err != nil {
		return false
	}
	response := strings.ToLower(strings.TrimSpace(line))
	return response == "y" || response == "yes"
}

func (s *Shell) createProgressBar(remotePath string) ProgressCallback {
	// 尝试 Stat 获取大小
	info, err := s.client.sftpClient.Stat(remotePath)
	total := int64(-1)
	description := "Downloading"
	if err == nil {
		if !info.IsDir() {
			total = info.Size()
		} else {
			description = "Downloading (Dir)"
		}
	}

	bar := progressbar.NewOptions64(
		total,
		progressbar.OptionSetDescription(description),
		progressbar.OptionSetWriter(s.rl.Stdout()),
		progressbar.OptionEnableColorCodes(true),
		progressbar.OptionShowBytes(true),
		progressbar.OptionSetWidth(30),
		progressbar.OptionThrottle(65*time.Millisecond),
		progressbar.OptionOnCompletion(func() {
			_, _ = fmt.Fprint(s.rl.Stdout(), "\n")
		}),
	)
	return func(n int64) { _ = bar.Add64(n) }
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// printColumns 多列格式输出，类似 Linux ls 命令
func (s *Shell) printColumns(names []string) {
	if len(names) == 0 {
		return
	}

	// 获取终端宽度
	width := 80 // 默认宽度
	if fd := int(os.Stdout.Fd()); term.IsTerminal(fd) {
		if w, _, err := term.GetSize(fd); err == nil && w > 0 {
			width = w
		}
	}

	// 找出最长名称
	maxLen := 0
	for _, name := range names {
		if len(name) > maxLen {
			maxLen = len(name)
		}
	}

	// 每列宽度 = 最大名称 + 2 (间距)
	colWidth := maxLen + 2
	if colWidth < 4 {
		colWidth = 4
	}

	// 计算列数
	cols := width / colWidth
	if cols < 1 {
		cols = 1
	}

	// 计算行数
	rows := (len(names) + cols - 1) / cols

	// 按列优先顺序输出
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			idx := col*rows + row
			if idx >= len(names) {
				break
			}
			name := names[idx]
			// 使用固定宽度格式化，左对齐
			_, _ = fmt.Fprintf(s.rl.Stdout(), "%-*s", colWidth, name)
		}
		_, _ = fmt.Fprintln(s.rl.Stdout())
	}
}
