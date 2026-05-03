package git

import (
	"ccrctl/pkg/api/coding"
	"ccrctl/pkg/config"
	"ccrctl/pkg/logger"
	"ccrctl/pkg/system"
	"fmt"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"time"
)

const (
	SourceOriginName                = "source"
	ListAllBranchesAndGrep          = "git branch -a | grep -E '(^|/)%s$'"
	CheckoutBranch                  = "git checkout %s --"
	RebaseBranch                    = "git rebase %s"
	ForcePushBranch                 = "git push -f"
	CNBYamlFileName                 = ".cnb.yml"
	SetCheckOutDefaultRemoteCommand = " git config --global checkout.defaultRemote origin"
	GetOriginBranchesCommand        = "git branch -r | grep '^  origin/'"
	GitPushToLocalBareRepo          = "git push -f source"
	ErrInvalidUpstreamEN            = "fatal: invalid upstream"
	ErrInvalidUpstreamZH            = "致命错误：无效的上游"
	pushBranchCMD                   = "git push %s refs/heads/*:refs/heads/*"
	pushTagForceCMD                 = "git push -f %s refs/tags/*:refs/tags/*"
	pushForceCMD                    = "git push -f %s refs/heads/*:refs/heads/* refs/tags/*:refs/tags/*"
	lfsPushCMD                      = "git lfs push --all %s"
	lfsLsFilesAllCMD                = "git lfs ls-files --all"
)

var FileLimitSize = config.Cfg.GetString("migrate.file_limit_size")

// Clone 镜像克隆Git仓库（带重试机制）
// 参数:
//   - cloneURL: Git仓库克隆地址
//   - repoPath: 本地仓库路径
//   - allowIncompletePush: 是否允许不完整的LFS推送
//
// 返回值:
//   - error: 克隆失败时返回错误信息
//
// 重试机制: 失败时会自动重试3次，重试间隔分别为1秒、5秒、10秒
func Clone(cloneURL, repoPath string, allowIncompletePush bool) error {
	logger.Logger.Infof("%s 开始clone", repoPath)
	// 重试间隔配置：第1次失败后等1秒，第2次失败后等5秒，第3次失败后等10秒
	retryIntervals := []time.Duration{1 * time.Second, 5 * time.Second, 10 * time.Second}
	var out string
	var err error

	// 重试循环：最多尝试3次
	for i, interval := range retryIntervals {
		cmd := fmt.Sprintf("git clone --mirror %s %s", cloneURL, repoPath)
		logger.Logger.Debugf(cmd)
		logger.Logger.Infof("%s git 克隆中... (尝试 %d/%d)", repoPath, i+1, len(retryIntervals))
		out, err = system.ExecCommand(cmd, "./")
		if err == nil {
			// 克隆成功，跳出重试循环
			break
		}
		// 克隆失败，屏蔽错误日志中的敏感信息（如Git凭证）
		maskedOutput := removeCredentialsFromURL(out)
		logger.Logger.Warnf("%s git clone 失败 (尝试 %d/%d): %v \n %s", repoPath, i+1, len(retryIntervals), err, maskedOutput)
		// 如果不是最后一次尝试，则等待指定时间后重试
		if i < len(retryIntervals)-1 {
			time.Sleep(interval)
		}
	}

	// 所有重试都失败，返回错误
	if err != nil {
		maskedOutput := removeCredentialsFromURL(out)
		return fmt.Errorf("%s clone失败: %s\n %s", repoPath, err, maskedOutput)
	}

	// 克隆成功后，下载LFS文件
	out, err = FetchLFS(repoPath, allowIncompletePush)
	if err != nil {
		return fmt.Errorf("%s 下载LFS文件失败: %s\n %s", repoPath, err, out)
	}
	logger.Logger.Infof("%s clone成功", repoPath)
	return nil
}

// NormalClone 普通克隆Git仓库（带重试机制）
// 参数:
//   - cloneURL: Git仓库克隆地址
//   - repoPath: 本地仓库路径
//
// 返回值:
//   - error: 克隆失败时返回错误信息
//
// 重试机制: 失败时会自动重试3次，重试间隔分别为1秒、5秒、10秒
func NormalClone(cloneURL, repoPath string) error {
	logger.Logger.Infof("%s 开始clone", repoPath)
	// 重试间隔配置：第1次失败后等1秒，第2次失败后等5秒，第3次失败后等10秒
	retryIntervals := []time.Duration{1 * time.Second, 5 * time.Second, 10 * time.Second}
	var out string
	var err error

	// 重试循环：最多尝试3次
	for i, interval := range retryIntervals {
		cmd := fmt.Sprintf("git clone %s %s", cloneURL, repoPath)
		logger.Logger.Debugf(cmd)
		logger.Logger.Infof("%s git 克隆中... (尝试 %d/%d)", repoPath, i+1, len(retryIntervals))
		out, err = system.ExecCommand(cmd, "./")
		if err == nil {
			// 克隆成功，直接返回
			logger.Logger.Infof("%s clone成功", repoPath)
			return nil
		}
		// 克隆失败，屏蔽错误日志中的敏感信息（如Git凭证）
		maskedOutput := removeCredentialsFromURL(out)
		logger.Logger.Warnf("%s git clone 失败 (尝试 %d/%d): %v \n %s", repoPath, i+1, len(retryIntervals), err, maskedOutput)
		// 如果不是最后一次尝试，则等待指定时间后重试
		if i < len(retryIntervals)-1 {
			time.Sleep(interval)
		}
	}

	// 所有重试都失败，返回错误
	maskedOutput := removeCredentialsFromURL(out)
	return fmt.Errorf("%s clone失败: %s\n %s", repoPath, err, maskedOutput)
}

// NormalCloneWithOutput 执行Git克隆操作并返回输出内容（带重试机制）
// 用于需要检查克隆输出信息的场景（如空仓库检测）
//
// 参数:
//   - cloneURL: Git仓库克隆地址
//   - repoPath: 本地仓库路径
//
// 返回值:
//   - string: Git命令的输出内容（成功或失败时都会返回，失败时已屏蔽敏感信息）
//   - error: 克隆失败时返回错误信息
//
// 重试机制: 失败时会自动重试3次，重试间隔分别为1秒、5秒、10秒
func NormalCloneWithOutput(cloneURL, repoPath string) (string, error) {
	logger.Logger.Infof("%s 开始clone", repoPath)
	// 重试间隔配置：第1次失败后等1秒，第2次失败后等5秒，第3次失败后等10秒
	retryIntervals := []time.Duration{1 * time.Second, 5 * time.Second, 10 * time.Second}
	var out string
	var err error

	// 重试循环：最多尝试3次
	for i, interval := range retryIntervals {
		cmd := fmt.Sprintf("git clone %s %s", cloneURL, repoPath)
		logger.Logger.Debugf(cmd)
		logger.Logger.Infof("%s git 克隆中... (尝试 %d/%d)", repoPath, i+1, len(retryIntervals))
		out, err = system.ExecCommand(cmd, "./")
		if err == nil {
			// 克隆成功，返回原始输出内容（可能包含空仓库警告等信息）
			logger.Logger.Infof("%s clone成功", repoPath)
			return out, nil
		}
		// 克隆失败，屏蔽错误日志中的敏感信息（如Git凭证）
		maskedOutput := removeCredentialsFromURL(out)
		logger.Logger.Warnf("%s git clone 失败 (尝试 %d/%d): %v \n %s", repoPath, i+1, len(retryIntervals), err, maskedOutput)
		// 如果不是最后一次尝试，则等待指定时间后重试
		if i < len(retryIntervals)-1 {
			time.Sleep(interval)
		}
	}

	// 所有重试都失败，返回屏蔽敏感信息后的输出和错误
	maskedOutput := removeCredentialsFromURL(out)
	return maskedOutput, fmt.Errorf("%s clone失败: %s\n %s", repoPath, err, maskedOutput)
}

// IsEmptyRepositoryOutput 检查Git命令输出是否表明是空仓库（支持中英文环境）
// 该函数用于识别Git克隆操作输出中的空仓库相关警告信息
//
// 参数:
//   - output: Git命令的输出内容
//
// 返回值:
//   - bool: 如果输出表明是空仓库则返回true，否则返回false
//
// 支持的输出模式包括：
//   - 英文环境: "warning: you appear to have cloned an empty repository"等
//   - 中文环境: "警告：您似乎克隆了一个空仓库", "空仓库"等
//   - 其他空仓库指示信息
func IsEmptyRepositoryOutput(output string) bool {
	if output == "" {
		return false
	}

	outputStr := strings.ToLower(output)

	// 检查常见的空仓库输出信息（中英文）
	emptyRepoPatterns := []string{
		"warning: you appear to have cloned an empty repository",
		"警告：您似乎克隆了一个空仓库",
		"empty repository",
		"空仓库",
		"仓库为空",
		"repository is empty",
		"remote repository is empty",
	}

	for _, pattern := range emptyRepoPatterns {
		if strings.Contains(outputStr, pattern) {
			return true
		}
	}

	return false
}

func RebasePush(rebaseRepoPath string, rebaseSuccessBranches []string) error {
	for _, branchName := range rebaseSuccessBranches {
		checkBranchOut, CheckoutBranchErr := system.ExecCommand(fmt.Sprintf(CheckoutBranch, branchName), rebaseRepoPath)
		if CheckoutBranchErr != nil {
			logger.Logger.Errorf("%s 切换分支到%s失败: %s \n%s", rebaseRepoPath, branchName, CheckoutBranchErr, checkBranchOut)
			return CheckoutBranchErr
		}
		pushOut, err := system.ExecCommand(ForcePushBranch, rebaseRepoPath)
		if err != nil {
			return fmt.Errorf("%s %s rebase后 push失败: %s\n %s", rebaseRepoPath, branchName, err, pushOut)
		}
		logger.Logger.Infof("%s %s rebase后 push成功", rebaseRepoPath, branchName)
	}
	logger.Logger.Infof("%s rebase push成功", rebaseRepoPath)
	return nil
}

func Rebase(rebaseRepoPath, repoPath string) error {
	logger.Logger.Infof("%s 开始rebase", rebaseRepoPath)
	pwdDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("%s 获取当前目录失败: %s", rebaseRepoPath, err)
	}
	logger.Logger.Debugf("pwd: %s", pwdDir)
	bareRepoPath := path.Join(pwdDir, repoPath)
	logger.Logger.Debugf("bareRepoPath: %s", bareRepoPath)
	out, err := system.RunCommand("git", rebaseRepoPath, "remote", "add", SourceOriginName, bareRepoPath)
	if err != nil {
		return fmt.Errorf("%s 添加source远程仓库失败: %s\n %s", rebaseRepoPath, err, out)
	}
	logger.Logger.Infof("%s 添加source远程仓库成功", rebaseRepoPath)
	out, err = system.RunCommand("git", rebaseRepoPath, "fetch", SourceOriginName)
	if err != nil {
		return fmt.Errorf("%s 拉取souce远程仓库失败: %s\n %s", rebaseRepoPath, err, out)
	}
	logger.Logger.Infof("%s 拉取souce远程仓库成功", rebaseRepoPath)
	// 如果没有指定rebaseBranches, 则遍历所有分支进行处理
	// 获取所有远程分支
	out, err = system.ExecCommand(GetOriginBranchesCommand, rebaseRepoPath)
	if err != nil {
		return fmt.Errorf("%s 获取远程分支列表失败: %s\n %s", rebaseRepoPath, err, out)
	}

	// 解析分支列表，过滤掉 HEAD 和 origin/HEAD
	remoteBranches := strings.Split(strings.TrimSpace(out), "\n")
	var branches []string
	for _, branch := range remoteBranches {
		branch = strings.TrimSpace(branch)
		if strings.Contains(branch, "->") || strings.Contains(branch, "HEAD") {
			continue
		}
		// 去除 "origin/" 前缀
		branch = strings.TrimPrefix(branch, "origin/")
		branches = append(branches, branch)
	}
	logger.Logger.Infof("%s 分支列表: %s", rebaseRepoPath, branches)

	// 遍历所有分支进行rebase
	for _, branch := range branches {
		// 切换到指定分支
		checkBranchErr := checkoutBranch(rebaseRepoPath, branch)
		if checkBranchErr != nil {
			return fmt.Errorf("%s 分支 %s checkout失败: %s", rebaseRepoPath, branch, checkBranchErr)
		}
		// 检查 .cnb.yaml文件是否存在
		// CNBYamlFileAbsPath := path.Join(rebaseRepoPath, CNBYamlFileName)
		// exist := system.FileExists(CNBYamlFileAbsPath)
		// if !exist {
		// 	logger.Logger.Infof("%s分支%s .cnb.yml文件不存在,跳过 rebease", rebaseRepoPath, branch)
		// 	continue
		// }
		rebaseBranch := SourceOriginName + "/" + branch
		// rebase指定分支
		rebaseOut, rebaseErr := system.ExecCommand(fmt.Sprintf(RebaseBranch, rebaseBranch), rebaseRepoPath)
		if rebaseErr != nil {
			// 检查是否是分支不存在的情况
			if isInvalidUpstreamError(rebaseOut) {
				logger.Logger.Warnf("%s 分支 %s 在源仓库中不存在，跳过 rebase", rebaseRepoPath, branch)
				continue
			}
			return fmt.Errorf("仓库 %s 分支 %s rebase失败: %s\n %s", repoPath, branch, rebaseErr.Error(), rebaseOut)
		}
		logger.Logger.Infof("%s %s rebase成功", rebaseRepoPath, rebaseBranch)
		rebasePushOut, rebasePushErr := system.ExecCommand(GitPushToLocalBareRepo, rebaseRepoPath)
		if rebasePushErr != nil {
			return fmt.Errorf("分支 %s rebase后push失败: %s\n %s", branch, rebasePushErr.Error(), rebasePushOut)
		}
		logger.Logger.Infof("%s %s rebase后push成功", rebaseRepoPath, rebaseBranch)
	}
	return nil
}

func Push(repoPath, pushURL string, forcePush bool) (output string, err error) {
	logger.Logger.Infof("%s 开始push", repoPath)
	out, err := codePush(repoPath, pushURL, repoPath, forcePush)
	if err != nil {
		// 如果是大文件超限错误，使用 WARN 级别（系统会自动处理）
		if IsExceededLimitError(out) {
			logger.Logger.Warnf("%s 裸仓push失败(文件超过大小限制，系统将自动处理): %s", repoPath, err)
		} else {
			// 其他错误仍使用 ERROR 级别
			logger.Logger.Errorf("%s 裸仓push失败: %s", repoPath, err)
		}
		return out, err
	}
	logger.Logger.Infof("%s 裸仓push成功", repoPath)

	// 检查是否有LFS文件，如有则推送LFS
	hasLFSFiles, lfsCheckErr := hasLFSFiles(repoPath)
	if lfsCheckErr != nil {
		logger.Logger.Warnf("%s 检查LFS文件失败: %s，跳过LFS推送", repoPath, lfsCheckErr)
		return out, nil
	}

	if hasLFSFiles {
		logger.Logger.Infof("%s 检测到LFS文件", repoPath)
		out, err = PushLFS(repoPath, pushURL)
		if err != nil {
			return out, err
		}
	} else {
		logger.Logger.Infof("%s 未检测到LFS文件，跳过LFS推送", repoPath)
	}

	return out, nil
}

// 强制推送
func ForcePush(workDir, pushURL string) (output string, err error) {
	logger.Logger.Debugf(pushForceCMD, pushURL)
	output, err = system.ExecCommand(fmt.Sprintf(pushForceCMD, pushURL), workDir)
	if err != nil {
		return output, err
	}
	return output, nil
}

// removeCredentialsFromURL 屏蔽url中的敏感信息，如 git 凭证
func removeCredentialsFromURL(input string) string {
	// URL 正则表达式
	urlRegex := regexp.MustCompile(`https?://[^\s]+`)

	return urlRegex.ReplaceAllStringFunc(input, func(urlMatch string) string {
		// 使用 url.Parse 进行专业解析
		u, err := url.Parse(urlMatch)
		if err != nil {
			return urlMatch // 解析失败，返回原字符串
		}

		// 如果有用户信息，直接移除
		if u.User != nil {
			u.User = nil
			// 使用自定义的字符串构建方法
			return buildURLString(u)
		}

		return urlMatch
	})
}

// buildURLString 手动构建 URL 字符串，避免自动编码
func buildURLString(u *url.URL) string {
	var result strings.Builder

	result.WriteString(u.Scheme)
	result.WriteString("://")
	result.WriteString(u.Host)

	if u.Path != "" {
		result.WriteString(u.Path)
	}

	if u.RawQuery != "" {
		result.WriteString("?")
		result.WriteString(u.RawQuery)
	}

	if u.Fragment != "" {
		result.WriteString("#")
		result.WriteString(u.Fragment)
	}

	return result.String()
}

func codePush(workDir, pushURL, repoPath string, force bool) (output string, err error) {
	// 强制推送警告:提醒用户此操作的风险性
	if force {
		logger.Logger.Warnf("%s 即将执行强制推送(git push -f),此操作将覆盖目标仓库的历史记录,请确保您了解此操作的风险", repoPath)
		return pushWithRetry(workDir, repoPath, fmt.Sprintf(pushForceCMD, pushURL), nil)
	}

	branchOutput, err := pushWithRetry(workDir, repoPath, fmt.Sprintf(pushBranchCMD, pushURL), nil)
	if err != nil {
		return branchOutput, err
	}

	logger.Logger.Warnf("%s 即将强制同步tag到目标仓库，目标仓库同名tag将以源仓库为准", repoPath)
	tagOutput, err := pushWithRetry(workDir, repoPath, fmt.Sprintf(pushTagForceCMD, pushURL), nil)
	if err != nil {
		return branchOutput + tagOutput, err
	}

	return branchOutput + tagOutput, nil
}

func pushWithRetry(workDir, repoPath, cmd string, shouldSkipError func(string) bool) (output string, err error) {
	retryIntervals := []time.Duration{1 * time.Second, 5 * time.Second, 10 * time.Second}
	for i, interval := range retryIntervals {
		logger.Logger.Debugf(cmd)
		logger.Logger.Infof("%s git 推送中... (尝试 %d/%d)", repoPath, i+1, len(retryIntervals))
		output, err = system.ExecCommand(cmd, workDir)
		if err == nil {
			return output, nil
		}
		// 屏蔽错误日志中的敏感信息
		output = removeCredentialsFromURL(output)
		if shouldSkipError != nil && shouldSkipError(output) {
			return output, nil
		}
		logger.Logger.Warnf("%s git push 失败 (尝试 %d/%d): %v \n %s", repoPath, i+1, len(retryIntervals), err, output)
		if i < len(retryIntervals)-1 {
			time.Sleep(interval)
		}
	}
	return output, err
}

func IsLFSRepo(repoPath string) (error, bool) {
	workDir := repoPath
	output, err := system.RunCommand("git", workDir, "lfs", "ls-files", "--all")
	logger.Logger.Debugf("%s 检查是否是LFS仓库\n%s", repoPath, output)
	if err != nil {
		return err, false
	}
	return nil, len(output) > 0
}

// hasLFSFiles 检查仓库是否有LFS文件
func hasLFSFiles(repoPath string) (bool, error) {
	logger.Logger.Debugf("%s 检查是否有LFS文件", repoPath)

	// 使用 git lfs ls-files --all 检查是否有实际的LFS文件
	output, err := system.ExecCommand(lfsLsFilesAllCMD, repoPath)
	if err != nil {
		logger.Logger.Errorf("%s 执行git lfs ls-files --all失败: %s", repoPath, err)
		return false, err
	}

	// 去除空白字符后检查是否有内容
	trimmedOutput := strings.TrimSpace(output)
	hasFiles := len(trimmedOutput) > 0

	if hasFiles {
		logger.Logger.Debugf("%s 检测到LFS文件:\n%s", repoPath, trimmedOutput)
	} else {
		logger.Logger.Debugf("%s 未检测到LFS文件", repoPath)
	}

	return hasFiles, nil
}

// FetchLFS 下载 LFS 文件（带重试机制）
// 参数:
//   - repoPath: 本地仓库路径
//   - allowIncompletePush: 是否允许不完整的 LFS 推送
//
// 返回值:
//   - string: 命令输出（已屏蔽敏感信息）
//   - error: 错误信息
//
// 重试机制: 失败时会自动重试 3 次，重试间隔分别为 2 秒、5 秒、10 秒
func FetchLFS(repoPath string, allowIncompletePush bool) (string, error) {
	workDir := repoPath
	logger.Logger.Infof("%s 开始下载 LFS 文件", repoPath)

	// 重试间隔配置：第 1 次失败后等 2 秒，第 2 次失败后等 5 秒，第 3 次失败后等 10 秒
	retryIntervals := []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second}
	var output string
	var err error

	// 重试循环：最多尝试 3 次
	for i, interval := range retryIntervals {
		logger.Logger.Infof("%s 下载 LFS 文件中... (尝试 %d/%d)", repoPath, i+1, len(retryIntervals))
		output, err = system.RunCommand("git", workDir, "lfs", "fetch", "--all", "origin")

		if err == nil {
			// 下载成功
			logger.Logger.Infof("%s LFS 文件下载成功", repoPath)
			maskedOutput := removeCredentialsFromURL(output)
			logger.Logger.Debugf("%s LFS 文件下载输出:\n%s", repoPath, maskedOutput)
			return maskedOutput, nil
		}

		// 下载失败，屏蔽日志输出中的敏感信息
		maskedOutput := removeCredentialsFromURL(output)
		logger.Logger.Warnf("%s LFS 文件下载失败 (尝试 %d/%d): %v\n%s", repoPath, i+1, len(retryIntervals), err, maskedOutput)

		// 如果不是最后一次尝试，则等待指定时间后重试
		if i < len(retryIntervals)-1 {
			logger.Logger.Infof("%s 等待 %v 后重试...", repoPath, interval)
			time.Sleep(interval)
		}
	}

	// 所有重试都失败
	maskedOutput := removeCredentialsFromURL(output)

	// 如果允许不完整推送，且确认是源文件损坏错误，设置对应配置并继续
	if allowIncompletePush {
		// 检查是否是 LFS 源文件损坏/丢失的错误
		if IsLFSObjectNotFoundError(output) {
			logger.Logger.Warnf("%s 检测到 LFS 源文件损坏/丢失错误（重试 %d 次后），由于开启了 allow_incomplete_push，将忽略错误继续迁移", repoPath, len(retryIntervals))
			logger.Logger.Infof("%s 正在设置 lfs.allowincompletepush=true", repoPath)
			configOutput, configErr := system.RunCommand("git", workDir, "config", "lfs.allowincompletepush", "true")
			if configErr != nil {
				return removeCredentialsFromURL(configOutput), fmt.Errorf("设置 lfs.allowincompletepush 失败: %w", configErr)
			}
			logger.Logger.Warnf("%s 已设置 lfs.allowincompletepush=true，将尝试推送已下载的 LFS 文件", repoPath)
			return maskedOutput, nil
		} else {
			// 不是源文件损坏错误（可能是网络、权限等可恢复的问题）
			logger.Logger.Errorf("%s LFS 文件下载失败（重试 %d 次后），错误类型不是源文件损坏，建议检查网络、权限等问题后重试", repoPath, len(retryIntervals))
			logger.Logger.Errorf("%s 错误详情: %v\n%s", repoPath, err, maskedOutput)
			return maskedOutput, fmt.Errorf("LFS 文件下载失败（非源文件损坏错误）: %w", err)
		}
	}

	// 不允许不完整推送，返回错误
	logger.Logger.Errorf("%s LFS 文件下载失败（重试 %d 次后）", repoPath, len(retryIntervals))
	return maskedOutput, fmt.Errorf("LFS 文件下载失败: %w", err)
}

// PushLFS 推送 LFS 文件到远程仓库（带重试机制）
// 参数:
//   - repoPath: 本地仓库路径
//   - pushUrl: 推送目标 URL
//
// 返回值:
//   - string: 命令输出（已屏蔽敏感信息）
//   - error: 错误信息
//
// 重试机制: 失败时会自动重试 3 次，重试间隔分别为 2 秒、5 秒、10 秒
func PushLFS(repoPath, pushUrl string) (string, error) {
	logger.Logger.Infof("%s 开始推送 LFS 文件", repoPath)
	workDir := repoPath

	// 重试间隔配置：第 1 次失败后等 2 秒，第 2 次失败后等 5 秒，第 3 次失败后等 10 秒
	retryIntervals := []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second}
	var output string
	var err error

	// 重试循环：最多尝试 3 次
	for i, interval := range retryIntervals {
		logger.Logger.Infof("%s 推送 LFS 文件中... (尝试 %d/%d)", repoPath, i+1, len(retryIntervals))
		output, err = system.ExecCommand(fmt.Sprintf(lfsPushCMD, pushUrl), workDir)

		if err == nil {
			// 推送成功
			logger.Logger.Infof("%s LFS 文件推送成功", repoPath)
			return output, nil
		}

		// 推送失败，屏蔽输出中的敏感信息
		maskedOutput := removeCredentialsFromURL(output)
		logger.Logger.Warnf("%s LFS 文件推送失败 (尝试 %d/%d): %v\n%s", repoPath, i+1, len(retryIntervals), err, maskedOutput)

		// 如果不是最后一次尝试，则等待指定时间后重试
		if i < len(retryIntervals)-1 {
			logger.Logger.Infof("%s 等待 %v 后重试...", repoPath, interval)
			time.Sleep(interval)
		}
	}

	// 所有重试都失败
	maskedOutput := removeCredentialsFromURL(output)
	logger.Logger.Errorf("%s LFS 文件推送失败（重试 %d 次后）", repoPath, len(retryIntervals))
	return maskedOutput, fmt.Errorf("LFS 文件推送失败: %w", err)
}

func FixExceededLimitError(repoPath string) error {
	workDir := repoPath
	above := "--above=" + FileLimitSize + "Mb"
	logger.Logger.Infof("%s 使用git lfs migrate 处理历史提交中的大文件", repoPath)
	output, err := system.RunCommand("git", workDir, "lfs", "migrate", "import", "--everything", above)
	if err != nil {
		// 屏蔽输出中的敏感信息
		maskedOutput := removeCredentialsFromURL(output)
		return fmt.Errorf("git lfs migrate import 失败: %s\n%s", err, maskedOutput)
	}
	logger.Logger.Infof("%s 使用git lfs migrate 处理历史提交中的大文件成功", repoPath)
	logger.Logger.Debugf("%s 使用git lfs migrate 处理历史提交中的大文件成功\n%s", repoPath, output)
	return nil
}

func IsExceededLimitError(output string) bool {
	if strings.Contains(output, "exceeded limit") {
		return true
	}
	return false
}

// IsLFSObjectNotFoundError 判断是否是 LFS 源文件损坏/丢失的错误
// 只匹配 "Repository or object not found" 错误，这是最典型的源文件丢失错误
func IsLFSObjectNotFoundError(output string) bool {
	return strings.Contains(strings.ToLower(output), "repository or object not found")
}

func IsSvnRepo(vcsType string) bool {
	if vcsType == coding.SvnVcsType {
		return true
	}
	return false
}

func SetCheckOutDefaultRemote() error {
	output, err := system.ExecCommand(SetCheckOutDefaultRemoteCommand, ".")
	if err != nil {
		return fmt.Errorf("git config remote.origin.fetch 失败: %s\n%s", err, output)
	}
	return nil
}

func checkoutBranch(repoPath, branch string) error {
	_, err := system.ExecCommand(fmt.Sprintf(CheckoutBranch, branch), repoPath)
	if err != nil {
		logger.Logger.Warnf(fmt.Sprintf("%s 切换分支 %s 失败,尝试指定ref切换", repoPath, branch))
		refBranch := "refs/remotes/origin/" + branch
		// 兼容分支名匹配到 tree object 问题
		out, refErr := system.ExecCommand(fmt.Sprintf(CheckoutBranch, refBranch), repoPath)
		if refErr != nil {
			logger.Logger.Errorf(fmt.Sprintf("%s 指定ref切换分支 %s 失败: %s\n%s", repoPath, refBranch, refErr, out))
			return refErr
		}
	}
	return nil
}

// isInvalidUpstreamError 检查是否是分支不存在的错误（同时处理英文和中文环境）
func isInvalidUpstreamError(output string) bool {
	return strings.Contains(output, ErrInvalidUpstreamEN) ||
		strings.Contains(output, ErrInvalidUpstreamZH)
}

// IsBareRepoInitialized 判断本地裸仓库是否初始化（有分支或tag）
// repoDir: 本地裸仓库目录
func IsBareRepoInitialized(repoDir string) bool {
	// 执行 git for-each-ref，若有输出则说明已初始化
	output, err := system.ExecCommand("git for-each-ref", repoDir)
	if err != nil {
		logger.Logger.Warnf("检测裸仓库初始化状态失败: %s", err)
		return false
	}
	return len(output) > 0
}
