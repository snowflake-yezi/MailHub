package mailbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Manager 邮箱账户管理（操作 Dovecot userdb + Postfix virtual）
type Manager struct {
	mu            sync.RWMutex
	commandRunner func(name string, args ...string) error
	maildirBase   string // Maildir 基础路径
	usersFile     string // Dovecot passwd-file 路径
	vmailboxFile  string // Postfix virtual_mailbox_maps 路径
	vmailUID      int    // Maildir 属主 UID（默认 5000，宝塔共存机用 150）
	vmailGID      int    // Maildir 属组 GID
}

func defaultSystemCommand(name string, args ...string) error {
	output, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

// MaildirBase 返回 Maildir 基础路径
func (m *Manager) MaildirBase() string {
	return m.maildirBase
}

// ActiveCount 返回本节点当前活跃邮箱账号数（Dovecot users.conf 中的有效行数）。
// 供心跳上报为 load，供 mgmt 周期性校准 mail_servers.current_load。
func (m *Manager) ActiveCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	data, err := os.ReadFile(m.usersFile)
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		// 有效行形如 "user@domain:{PLAIN}pass::::::"
		if line != "" && strings.Contains(line, ":") {
			n++
		}
	}
	return n
}

// NewManager 创建邮箱管理器
func NewManager(maildirBase string, vmailUID, vmailGID int) *Manager {
	return &Manager{
		maildirBase:   maildirBase,
		usersFile:     "/etc/dovecot/users.conf",
		vmailboxFile:  "/etc/postfix/vmailbox",
		vmailUID:      vmailUID,
		vmailGID:      vmailGID,
		commandRunner: defaultSystemCommand,
	}
}

// NewManagerWithFiles 创建邮箱管理器，并允许测试注入配置文件路径。
func NewManagerWithFiles(maildirBase string, vmailUID, vmailGID int, usersFile, vmailboxFile string) *Manager {
	m := NewManager(maildirBase, vmailUID, vmailGID)
	m.usersFile = usersFile
	m.vmailboxFile = vmailboxFile
	m.commandRunner = func(string, ...string) error { return nil }
	return m
}

// SetCommandRunner replaces the operating-system command executor.
func (m *Manager) SetCommandRunner(runner func(name string, args ...string) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if runner == nil {
		runner = defaultSystemCommand
	}
	m.commandRunner = runner
}

// mailboxExists 检查邮箱是否已存在（Dovecot users.conf 里有记录）
func (m *Manager) mailboxExists(email string) bool {
	data, err := os.ReadFile(m.usersFile)
	if err != nil {
		return false
	}
	prefix := email + ":"
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

// vmailboxExists 检查邮箱是否已在 Postfix vmailbox 中有记录。
func (m *Manager) vmailboxExists(email string) bool {
	data, err := os.ReadFile(m.vmailboxFile)
	if err != nil {
		return false
	}
	prefix := email + " "
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

// MailboxInfo 邮箱信息
type MailboxInfo struct {
	EmailAddress string `json:"email_address"`
	Domain       string `json:"domain"`
	LocalPart    string `json:"local_part"`
	MaildirPath  string `json:"maildir_path"`
}

// Create 创建邮箱
// 1. 在 Dovecot users.conf 添加账号
// 2. 在 Postfix vmailbox 添加记录
// 3. 创建 Maildir 目录
// 4. 重新加载 Postfix
func (m *Manager) Create(email, password string) (*MailboxInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	localPart, domain, err := validateMailboxAddress(email)
	if err != nil {
		return nil, fmt.Errorf("invalid email address %q: %w", email, err)
	}
	if err := validateMailboxPassword(password); err != nil {
		return nil, err
	}

	info := &MailboxInfo{
		EmailAddress: email,
		Domain:       domain,
		LocalPart:    localPart,
		MaildirPath:  filepath.Join(m.maildirBase, domain, localPart),
	}

	// 1. 创建 Maildir 目录结构
	maildirDirs := []string{"cur", "new", "tmp"}
	for _, sub := range maildirDirs {
		dir := filepath.Join(info.MaildirPath, sub)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, fmt.Errorf("create maildir %s: %w", dir, err)
		}
	}
	// 递归设置属主：从 domain 目录起 chown（覆盖 MkdirAll 以 root 建的 domain 层 + 本邮箱子树）。
	// 之前只 chown mailbox 子树(info.MaildirPath)，漏了 domain 层 → virtual 进程(vmailUID) 进不去
	// domain 目录 → 投递 Permission denied。干净机(非宝塔)每个新域首个邮箱都会触发。
	if runtime.GOOS != "windows" {
		domainDir := filepath.Join(m.maildirBase, domain)
		if err := filepath.Walk(domainDir, func(p string, _ os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			return os.Chown(p, m.vmailUID, m.vmailGID)
		}); err != nil {
			return nil, fmt.Errorf("chown maildir: %w", err)
		}
	}

	// 2. 添加到 Dovecot users.conf
	// 格式: email:{SHA512-CRYPT}password::::::
	if !m.mailboxExists(email) {
		entry := fmt.Sprintf("%s:{PLAIN}%s::::::\n", email, password)
		if err := m.appendToFile(m.usersFile, entry); err != nil {
			return nil, fmt.Errorf("add dovecot user: %w", err)
		}
	}

	// 3. 添加到 Postfix virtual mailbox maps
	if !m.vmailboxExists(email) {
		vmailEntry := fmt.Sprintf("%s %s/\n", email, filepath.Join(domain, localPart))
		if err := m.appendToFile(m.vmailboxFile, vmailEntry); err != nil {
			return nil, fmt.Errorf("add postfix vmailbox: %w", err)
		}
	}

	// 4. 重新生成 postfix 哈希表并重载
	if err := m.commandRunner("postmap", m.vmailboxFile); err != nil {
		return nil, err
	}
	if err := m.commandRunner("postfix", "reload"); err != nil {
		return nil, err
	}

	return info, nil
}

// UpdatePassword rewrites a user's password line in Dovecot users.conf.
// It reads the file, replaces the matching line, writes atomically (.tmp → rename),
// and runs doveadm reload.
func (m *Manager) UpdatePassword(email, newPassword string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, _, err := validateMailboxAddress(email); err != nil {
		return err
	}
	if err := validateMailboxPassword(newPassword); err != nil {
		return err
	}
	if !m.mailboxExists(email) {
		return fmt.Errorf("mailbox not found: %s", email)
	}

	data, err := os.ReadFile(m.usersFile)
	if err != nil {
		return fmt.Errorf("read users.conf: %w", err)
	}

	prefix := email + ":"
	newEntry := fmt.Sprintf("%s:{PLAIN}%s::::::", email, newPassword)

	var lines []string
	found := false
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue // skip empty trailing lines
		}
		if strings.HasPrefix(line, prefix) {
			lines = append(lines, newEntry)
			found = true
		} else {
			lines = append(lines, line)
		}
	}
	if !found {
		return fmt.Errorf("mailbox entry not found in users.conf: %s", email)
	}

	if err := writeFileAtomic(m.usersFile, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		return fmt.Errorf("replace users.conf: %w", err)
	}

	// Reload Dovecot so the new password takes effect immediately.
	return m.commandRunner("doveadm", "reload")
}

// Delete 安全删除邮箱（软删除：Rename 到 .trash/ 而非 rm -rf）。
// 协议见 forwarding-design.md §9。
//
// 调用方如需完整的"摘除 Postfix/Dovecot → 等待转发排空 → 软删除"协议，
// 请使用 forward.Lifecycle.MoveToTrash 代替本方法。
func (m *Manager) Delete(email string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	localPart, domain, err := ParseAddress(email)
	if err != nil {
		return err
	}

	maildirPath := filepath.Join(m.maildirBase, domain, localPart)

	// Verify the mailbox exists
	if _, err := os.Stat(maildirPath); os.IsNotExist(err) {
		return fmt.Errorf("mailbox not found: %s", email)
	}

	// Remove from Postfix & Dovecot configs
	if err := m.removeFromConfigsLocked(email); err != nil {
		return err
	}

	// Atomically move to .trash/ — does not break Postfix virtual(8)
	// or forwarding goroutines holding file descriptors.
	trashBase := filepath.Join(m.maildirBase, ".trash")
	if err := os.MkdirAll(trashBase, 0700); err != nil {
		return fmt.Errorf("mkdir .trash: %w", err)
	}

	trashName := fmt.Sprintf("%s-%d", localPart, time.Now().Unix())
	trashPath := filepath.Join(trashBase, trashName)

	if err := os.Rename(maildirPath, trashPath); err != nil {
		return fmt.Errorf("rename to trash: %w", err)
	}

	return nil
}

// RemoveFromConfigs removes an email address from Postfix virtual_mailbox_maps
// and Dovecot users.conf, then reloads Postfix. New mail to this address will
// bounce rather than land in a missing directory.
func (m *Manager) RemoveFromConfigs(email string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.removeFromConfigsLocked(email)
}

func (m *Manager) removeFromConfigsLocked(email string) error {
	if _, _, err := validateMailboxAddress(email); err != nil {
		return err
	}
	// 1. Remove from Postfix virtual mailbox maps
	if err := m.removeLineFromFile(m.vmailboxFile, email); err != nil {
		return fmt.Errorf("postfix vmailbox: %w", err)
	}
	if err := m.commandRunner("postmap", m.vmailboxFile); err != nil {
		return err
	}
	if err := m.commandRunner("postfix", "reload"); err != nil {
		return err
	}

	// 2. Remove from Dovecot users.conf
	if err := m.removeLineFromFile(m.usersFile, email); err != nil {
		return fmt.Errorf("dovecot users: %w", err)
	}

	return m.commandRunner("doveadm", "reload")
}

// ReinstallConfigs 把邮箱重新写回 Dovecot users.conf + Postfix vmailbox 并 reload，
// 是 RemoveFromConfigs 的逆操作。restore 把 Maildir 从 .trash 回迁后调用，
// 恢复邮箱的收信/登录能力。前提：配置行已在 MoveToTrash 阶段被 RemoveFromConfigs 摘除。
// 幂等：若 Dovecot/Postfix 行异常残留则不重复追加。
func (m *Manager) ReinstallConfigs(email, password string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	localPart, domain, err := validateMailboxAddress(email)
	if err != nil {
		return err
	}
	if err := validateMailboxPassword(password); err != nil {
		return err
	}

	// Dovecot users.conf：幂等，异常残留时不重复追加
	if !m.mailboxExists(email) {
		entry := fmt.Sprintf("%s:{PLAIN}%s::::::\n", email, password)
		if err := m.appendToFile(m.usersFile, entry); err != nil {
			return fmt.Errorf("add dovecot user: %w", err)
		}
	}

	// Postfix virtual mailbox maps：幂等，异常残留时不重复追加
	if !m.vmailboxExists(email) {
		vmailEntry := fmt.Sprintf("%s %s/\n", email, domain+"/"+localPart)
		if err := m.appendToFile(m.vmailboxFile, vmailEntry); err != nil {
			return fmt.Errorf("add postfix vmailbox: %w", err)
		}
	}

	if err := m.commandRunner("postmap", m.vmailboxFile); err != nil {
		return err
	}
	if err := m.commandRunner("postfix", "reload"); err != nil {
		return err
	}
	if err := m.commandRunner("doveadm", "reload"); err != nil {
		return err
	}
	return nil
}

// ChownMaildirTree recursively sets ownership on the domain directory that contains
// the mailbox, matching Create's ownership repair for domain + mailbox layers.
func (m *Manager) ChownMaildirTree(domain string) error {
	if !validDNSName(strings.ToLower(domain)) {
		return fmt.Errorf("invalid domain")
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	domainDir := filepath.Join(m.maildirBase, domain)
	return filepath.Walk(domainDir, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Chown(p, m.vmailUID, m.vmailGID)
	})
}

// removeLineFromFile rewrites a file without lines containing the given substring.
func (m *Manager) removeLineFromFile(path, substr string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var kept []string
	for _, line := range strings.Split(string(data), "\n") {
		if !configLineMatchesEmail(line, substr) {
			kept = append(kept, line)
		}
	}

	return writeFileAtomic(path, []byte(strings.Join(kept, "\n")), 0644)
}

func configLineMatchesEmail(line, email string) bool {
	return strings.HasPrefix(line, email+":") || strings.HasPrefix(line, email+" ")
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mailhub-config-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// appendToFile 追加一行到文件
func (m *Manager) appendToFile(path, line string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := f.WriteString(line); err != nil {
		return err
	}
	return nil
}
