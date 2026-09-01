package core

// engine_cron.go — cron job execution and /cron command handlers
//
// Contains:
//   - ExecuteCronJob: runs an AI-prompt cron job
//   - executeCronShell / finishCronShell: runs shell-command cron jobs
//   - cronRunTitle: helper that derives a display title from a CronJob
//   - cmdCron*: /cron sub-command handlers (add, list, del, enable, disable, mute, setup)
//
// All methods remain func (e *Engine) receivers so they can access Engine
// fields and helpers defined in engine.go (same package).

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// ExecuteCronJob runs a cron job by injecting a synthetic message into the engine.
// It finds the platform that owns the session key, reconstructs a reply context,
// and processes the message as if the user sent it.
func (e *Engine) ExecuteCronJob(job *CronJob) error {
	e.hooks.Emit(HookEvent{
		Event:      HookEventCronTriggered,
		SessionKey: job.SessionKey,
		Content:    job.Prompt,
		Extra:      map[string]any{"job_id": job.ID, "job_description": job.Description},
	})

	sessionKey := job.SessionKey
	platformName := ""
	if idx := strings.Index(sessionKey, ":"); idx > 0 {
		platformName = sessionKey[:idx]
	}

	var targetPlatform Platform
	for _, p := range e.platforms {
		if p.Name() == platformName {
			targetPlatform = p
			break
		}
	}
	// Fallback: in multi-workspace mode the stored session key may be prefixed
	// with the workspace path (e.g. "/home/user/project:slack:C123:U456").
	// Search for a known platform name within the key and strip the prefix.
	if targetPlatform == nil {
		for _, p := range e.platforms {
			needle := ":" + p.Name() + ":"
			if idx := strings.Index(sessionKey, needle); idx >= 0 {
				targetPlatform = p
				platformName = p.Name()
				sessionKey = sessionKey[idx+1:] // strip workspace prefix
				break
			}
		}
	}
	if targetPlatform == nil {
		return fmt.Errorf("platform %q not found for session %q", platformName, sessionKey)
	}

	rc, ok := targetPlatform.(ReplyContextReconstructor)
	if !ok {
		return fmt.Errorf("platform %q does not support proactive messaging (cron)", platformName)
	}

	runSessionKey := sessionKey
	var replyCtx any
	var err error
	if !job.Mute {
		if resolver, ok := targetPlatform.(CronReplyTargetResolver); ok {
			resolvedSessionKey, resolvedReplyCtx, err := resolver.ResolveCronReplyTarget(sessionKey, cronRunTitle(job))
			if err != nil {
				if !errors.Is(err, ErrNotSupported) {
					return fmt.Errorf("resolve cron reply target: %w", err)
				}
			} else {
				if resolvedSessionKey != "" {
					runSessionKey = resolvedSessionKey
				}
				if resolvedReplyCtx != nil {
					replyCtx = resolvedReplyCtx
				}
			}
		}
	}
	if replyCtx == nil {
		replyCtx, err = rc.ReconstructReplyCtx(runSessionKey)
		if err != nil {
			return fmt.Errorf("reconstruct reply context: %w", err)
		}
	}

	// Wrap platform to discard all outgoing messages when muted
	effectivePlatform := targetPlatform
	if job.Mute {
		effectivePlatform = &mutePlatform{targetPlatform}
	}

	// Notify user that a cron job is executing (unless silent/muted).
	// Shell jobs skip this: the script alone decides whether to push anything.
	if !job.Mute && !job.IsShellJob() {
		silent := false
		if e.cronScheduler != nil {
			silent = e.cronScheduler.IsSilent(job)
		}
		if !silent {
			desc := job.Description
			if desc == "" {
				desc = truncateStr(job.Prompt, 40)
			}
			e.send(targetPlatform, replyCtx, fmt.Sprintf("⏰ %s", desc))
		}
	}

	if job.IsShellJob() {
		return e.executeCronShell(effectivePlatform, replyCtx, job)
	}

	// Resolve {{dashboard.*}} template variables (usage statistics) before
	// injecting the synthetic message. Unrecognized variables stay as-is.
	prompt := e.resolveDashboardTemplateVars(job.Prompt)

	msg := &Message{
		SessionKey:   sessionKey,
		Platform:     platformName,
		UserID:       "cron",
		UserName:     "cron",
		Content:      prompt,
		ReplyCtx:     replyCtx,
		ModeOverride: job.Mode,
	}

	// Resolve workspace-specific agent and sessions for multi-workspace mode.
	// Priority: job.WorkDir (explicit) > workspace binding > global agent fallback.
	agent := e.agent
	sessions := e.sessions
	workspaceDir := ""

	if e.multiWorkspace {
		channelID := extractChannelID(sessionKey)
		if channelID != "" {
			workspace, _, err := e.resolveWorkspace(targetPlatform, channelID)
			if err == nil && workspace != "" {
				wsAgent, wsSessions, _, effectiveDir, err := e.workspaceContext(workspace, sessionKey)
				if err == nil {
					agent = wsAgent
					sessions = wsSessions
					workspaceDir = effectiveDir
				}
			}
		}
	}

	if job.WorkDir != "" {
		wsAgent, wsSessions, err := e.getOrCreateWorkspaceAgent(job.WorkDir)
		if err == nil {
			agent = wsAgent
			sessions = wsSessions
			workspaceDir = job.WorkDir
		} else {
			slog.Warn("cron: workspace agent creation failed, using global",
				"work_dir", job.WorkDir, "session_key", sessionKey, "error", err)
		}
	}

	useNewSession := false
	if e.cronScheduler != nil {
		useNewSession = e.cronScheduler.UsesNewSession(job)
	} else {
		useNewSession = job.UsesNewSessionPerRun()
	}

	if useNewSession {
		msg.SessionKey = runSessionKey
		session := sessions.NewSideSession(runSessionKey, "cron-"+job.ID)
		if !session.TryLock() {
			return fmt.Errorf("session %q is busy", runSessionKey)
		}
		iKey := fmt.Sprintf("%s#cron:%s", runSessionKey, session.ID)
		if workspaceDir != "" {
			iKey = workspaceDir + ":" + iKey
		}
		e.processInteractiveMessageWith(effectivePlatform, msg, session, agent, sessions, iKey, workspaceDir, runSessionKey)
		e.cleanupInteractiveState(iKey)
		return nil
	}

	session := sessions.GetOrCreateActive(sessionKey)
	if !session.TryLock() {
		return fmt.Errorf("session %q is busy", sessionKey)
	}

	iKey := sessionKey
	if workspaceDir != "" {
		iKey = workspaceDir + ":" + sessionKey
	}
	e.processInteractiveMessageWith(effectivePlatform, msg, session, agent, sessions, iKey, workspaceDir, sessionKey)
	return nil
}

func cronRunTitle(job *CronJob) string {
	if job == nil {
		return "cron"
	}
	if desc := strings.TrimSpace(job.Description); desc != "" {
		return truncateStr(desc, 60)
	}
	if job.IsShellJob() {
		if cmd := strings.TrimSpace(job.Exec); cmd != "" {
			return truncateStr(cmd, 60)
		}
		return "cron"
	}
	if prompt := strings.TrimSpace(job.Prompt); prompt != "" {
		return truncateStr(prompt, 60)
	}
	return "cron"
}

// executeCronShell runs a shell command for a cron job and sends the output.
func (e *Engine) executeCronShell(p Platform, replyCtx any, job *CronJob) error {
	workDir := job.WorkDir
	if workDir == "" {
		if wd, ok := e.agent.(interface{ GetWorkDir() string }); ok {
			workDir = wd.GetWorkDir()
		}
	}
	if workDir == "" {
		workDir, _ = os.Getwd()
	}

	timeout := job.ExecutionTimeout()
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	cmdLabel := truncateStr(job.Exec, 60)

	ctx, cancel := context.WithTimeout(e.ctx, timeout)
	defer cancel()

	var shellCmd *exec.Cmd
	if runtime.GOOS == "windows" {
		shellCmd = exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", job.Exec)
	} else {
		shellCmd = exec.CommandContext(ctx, "sh", "-c", job.Exec)
	}
	shellCmd.Dir = workDir

	stdout, err := shellCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("shell: stdout pipe: %w", err)
	}
	stderr, err := shellCmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("shell: stderr pipe: %w", err)
	}

	if err := shellCmd.Start(); err != nil {
		e.send(p, replyCtx, fmt.Sprintf("⏰ ❌ `%s`\nerror: failed to start: %v", cmdLabel, err))
		return fmt.Errorf("shell: start: %w", err)
	}

	var mu sync.Mutex
	var buf bytes.Buffer
	doneCh := make(chan struct{})

	readPipe := func(r io.Reader) {
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 64*1024), 64*1024)
		for scanner.Scan() {
			mu.Lock()
			if buf.Len() > 0 {
				buf.WriteByte('\n')
			}
			buf.WriteString(scanner.Text())
			mu.Unlock()
		}
	}
	// Use a WaitGroup so both pipe-reader goroutines drain completely before
	// doneCh is closed. Without this, shellCmd.Wait() can return (closing the
	// pipe write-ends) while the scanners still have unread data in the OS
	// buffer, causing finishCronShell to read a truncated output.
	var pipeWg sync.WaitGroup
	pipeWg.Add(2)
	go func() { defer pipeWg.Done(); readPipe(stdout) }()
	go func() { defer pipeWg.Done(); readPipe(stderr) }()

	go func() {
		pipeWg.Wait()
		_ = shellCmd.Wait()
		close(doneCh)
	}()

	// Shell cron jobs run silently: the script decides whether to push a
	// message, so we never send an in-progress "⏰ ⏳" notification. Wait for
	// completion (or timeout), then let finishCronShell decide what to send.
	select {
	case <-doneCh:
		return e.finishCronShell(p, replyCtx, shellCmd, &mu, &buf, cmdLabel)
	case <-ctx.Done():
		killAndWait(shellCmd, doneCh)
		mu.Lock()
		output := buf.String()
		mu.Unlock()
		msg := fmt.Sprintf("⏰ ⚠️ timeout: `%s`", cmdLabel)
		if output != "" {
			msg = fmt.Sprintf("⏰ ⚠️ timeout: `%s`\n\n%s", cmdLabel, truncateStr(output, 3000))
		}
		e.send(p, replyCtx, msg)
		return fmt.Errorf("shell command timed out")
	}
}

func (e *Engine) finishCronShell(p Platform, replyCtx any, cmd *exec.Cmd, mu *sync.Mutex, buf *bytes.Buffer, cmdLabel string) error {
	mu.Lock()
	output := buf.String()
	mu.Unlock()

	exitOK := cmd.ProcessState.ExitCode() == 0

	// For shell jobs, the script decides whether to push a message: if it
	// succeeds with no stdout output, there is nothing to report, so skip
	// sending entirely (no "✅ (no output)" noise). Failures still report.
	if exitOK && strings.TrimSpace(output) == "" {
		return nil
	}

	var finalMsg string
	if exitOK {
		result := strings.TrimSpace(output)
		finalMsg = fmt.Sprintf("⏰ ✅ `%s`\n\n%s", cmdLabel, truncateStr(result, 3000))
	} else {
		errMsg := output
		if errMsg != "" {
			finalMsg = fmt.Sprintf("⏰ ❌ `%s`\n\n%s\n\nerror: exit code %d", cmdLabel, truncateStr(errMsg, 3000), cmd.ProcessState.ExitCode())
		} else {
			finalMsg = fmt.Sprintf("⏰ ❌ `%s`\n\nerror: exit code %d", cmdLabel, cmd.ProcessState.ExitCode())
		}
	}

	e.send(p, replyCtx, finalMsg)
	if !exitOK {
		return fmt.Errorf("shell: exit code %d", cmd.ProcessState.ExitCode())
	}
	return nil
}

// ──────────────────────────────────────────────────────────────
// /cron command
// ──────────────────────────────────────────────────────────────

func (e *Engine) cmdCron(p Platform, msg *Message, args []string) {
	if e.cronScheduler == nil {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgCronNotAvailable))
		return
	}

	if len(args) == 0 {
		if !supportsCards(p) {
			e.cmdCronList(p, msg)
			return
		}
		e.replyWithCard(p, msg.ReplyCtx, e.renderCronCard(msg.SessionKey, msg.UserID))
		return
	}

	sub := matchSubCommand(strings.ToLower(args[0]), []string{
		"add", "addexec", "list", "del", "delete", "rm", "remove", "enable", "disable", "mute", "unmute", "setup",
	})
	switch sub {
	case "add":
		e.cmdCronAdd(p, msg, args[1:])
	case "addexec":
		e.cmdCronAddExec(p, msg, args[1:])
	case "list":
		e.cmdCronList(p, msg)
	case "del", "delete", "rm", "remove":
		e.cmdCronDel(p, msg, args[1:])
	case "enable":
		e.cmdCronToggle(p, msg, args[1:], true)
	case "disable":
		e.cmdCronToggle(p, msg, args[1:], false)
	case "mute":
		e.cmdCronMute(p, msg, args[1:], true)
	case "unmute":
		e.cmdCronMute(p, msg, args[1:], false)
	case "setup":
		e.cmdCronSetup(p, msg)
	default:
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgCronUsage))
	}
}

func (e *Engine) cmdCronAdd(p Platform, msg *Message, args []string) {
	// /cron add <min> <hour> <day> <month> <weekday> <prompt...>
	if len(args) < 6 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgCronAddUsage))
		return
	}

	cronExpr := strings.Join(args[:5], " ")
	prompt := strings.Join(args[5:], " ")

	job := &CronJob{
		ID:         GenerateCronID(),
		Project:    e.name,
		SessionKey: msg.SessionKey,
		CronExpr:   cronExpr,
		Prompt:     prompt,
		Enabled:    true,
		CreatedAt:  time.Now(),
	}

	if err := e.cronScheduler.AddJob(job); err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgError, err))
		return
	}

	e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCronAdded), job.ID, cronExpr, truncateStr(prompt, 60)))
}

func (e *Engine) cmdCronAddExec(p Platform, msg *Message, args []string) {
	if !e.isAdmin(msg.UserID) {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgAdminRequired), "/cron addexec"))
		return
	}

	// /cron addexec <min> <hour> <day> <month> <weekday> <shell command...>
	if len(args) < 6 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgCronAddExecUsage))
		return
	}

	cronExpr := strings.Join(args[:5], " ")
	shellCmd := strings.Join(args[5:], " ")

	job := &CronJob{
		ID:         GenerateCronID(),
		Project:    e.name,
		SessionKey: msg.SessionKey,
		CronExpr:   cronExpr,
		Exec:       shellCmd,
		Enabled:    true,
		CreatedAt:  time.Now(),
	}

	if err := e.cronScheduler.AddJob(job); err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgError, err))
		return
	}

	e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCronAddedExec), job.ID, cronExpr, truncateStr(shellCmd, 60)))
}

func (e *Engine) cmdCronList(p Platform, msg *Message) {
	jobs := e.cronScheduler.Store().ListByProject(e.name)
	if len(jobs) == 0 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgCronEmpty))
		return
	}

	lang := e.i18n.CurrentLang()
	now := time.Now()
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(e.i18n.T(MsgCronListTitle), len(jobs)))
	sb.WriteString("\n")
	sb.WriteString("\n")

	for i, j := range jobs {
		if i > 0 {
			sb.WriteString("\n")
		}

		status := "✅"
		if !j.Enabled {
			status = "⏸"
		}
		desc := j.Description
		if desc == "" {
			if j.IsShellJob() {
				desc = "🖥 " + truncateStr(j.Exec, 60)
			} else {
				desc = truncateStr(j.Prompt, 60)
			}
		}
		if j.Mute {
			desc += " [mute]"
		}
		sb.WriteString(fmt.Sprintf("%s %s\n", status, desc))

		sb.WriteString(fmt.Sprintf("ID: %s\n", j.ID))

		human := CronExprToHuman(j.CronExpr, lang)
		sb.WriteString(e.i18n.Tf(MsgCronScheduleLabel, human, j.CronExpr))

		nextRun := e.cronScheduler.NextRun(j.ID)
		if !nextRun.IsZero() {
			fmtStr := cronTimeFormat(nextRun, now)
			sb.WriteString(e.i18n.Tf(MsgCronNextRunLabel, nextRun.Format(fmtStr)))
		}

		if !j.LastRun.IsZero() {
			fmtStr := cronTimeFormat(j.LastRun, now)
			sb.WriteString(e.i18n.Tf(MsgCronLastRunLabel, j.LastRun.Format(fmtStr)))
			if j.LastError != "" {
				sb.WriteString(fmt.Sprintf(" (failed: %s)", truncateStr(j.LastError, 40)))
			}
			sb.WriteString("\n")
		}
	}

	sb.WriteString(fmt.Sprintf("\n%s", e.i18n.T(MsgCronListFooter)))
	e.reply(p, msg.ReplyCtx, sb.String())
}

func (e *Engine) cmdCronDel(p Platform, msg *Message, args []string) {
	if len(args) == 0 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgCronDelUsage))
		return
	}
	id := args[0]
	if e.cronScheduler.RemoveJob(id) {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCronDeleted), id))
	} else {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCronNotFound), id))
	}
}

func (e *Engine) cmdCronToggle(p Platform, msg *Message, args []string, enable bool) {
	if len(args) == 0 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgCronDelUsage))
		return
	}
	id := args[0]
	var err error
	if enable {
		err = e.cronScheduler.EnableJob(id)
	} else {
		err = e.cronScheduler.DisableJob(id)
	}
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgError, err))
		return
	}
	if enable {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCronEnabled), id))
	} else {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCronDisabled), id))
	}
}

func (e *Engine) cmdCronMute(p Platform, msg *Message, args []string, mute bool) {
	if len(args) == 0 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgCronDelUsage))
		return
	}
	id := args[0]
	if !e.cronScheduler.Store().SetMute(id, mute) {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCronNotFound), id))
		return
	}
	if mute {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCronMuted), id))
	} else {
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCronUnmuted), id))
	}
}

func (e *Engine) cmdCronSetup(p Platform, msg *Message) {
	result, baseName, err := e.setupMemoryFile()
	switch result {
	case setupNative:
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgSetupNative))
	case setupNoMemory:
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgRelaySetupNoMemory))
	case setupExists:
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgRelaySetupExists), baseName))
	case setupError:
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgError, err))
	case setupOK:
		e.reply(p, msg.ReplyCtx, fmt.Sprintf(e.i18n.T(MsgCronSetupOK), baseName))
	}
}


// notifyCronFailure pushes a best-effort failure notice to the job's target
// session after all retries are exhausted. It is deliberately lightweight: it
// reconstructs a reply context and sends a single message; any error is logged
// and swallowed (a failure notice must never fail the scheduler).
func (e *Engine) notifyCronFailure(job *CronJob, runErr error) {
	if job == nil || runErr == nil {
		return
	}
	sessionKey := job.SessionKey
	platformName := ""
	if idx := strings.Index(sessionKey, ":"); idx > 0 {
		platformName = sessionKey[:idx]
	}
	var targetPlatform Platform
	for _, p := range e.platforms {
		if p.Name() == platformName {
			targetPlatform = p
			break
		}
	}
	if targetPlatform == nil {
		for _, p := range e.platforms {
			needle := ":" + p.Name() + ":"
			if idx := strings.Index(sessionKey, needle); idx >= 0 {
				targetPlatform = p
				platformName = p.Name()
				sessionKey = sessionKey[idx+1:]
				break
			}
		}
	}
	if targetPlatform == nil {
		return
	}
	rc, ok := targetPlatform.(ReplyContextReconstructor)
	if !ok {
		return
	}
	replyCtx, err := rc.ReconstructReplyCtx(sessionKey)
	if err != nil {
		slog.Warn("cron: failure-notice reply context failed", "job", job.ID, "error", err)
		return
	}
	desc := job.Description
	if desc == "" {
		desc = truncateStr(job.Prompt, 40)
	}
	msg := fmt.Sprintf("⚠️ 定时任务「%s」执行失败：%s", desc, truncateStr(runErr.Error(), 120))
	if err := targetPlatform.Send(e.ctx, replyCtx, msg); err != nil {
		slog.Warn("cron: failure-notice send failed", "job", job.ID, "error", err)
	}
}
