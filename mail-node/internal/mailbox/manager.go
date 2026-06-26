package mailbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Manager 邮箱账户管理（操作 Dovecot userdb + Postfix virtual）
type Manager struct {
	maildirBase  string // Maildir 基础路径
	usersFile    string // Dovecot passwd-file 路径
	vmailboxFile string // Postfix virtual_mailbox_maps 路径
	vmailUID     int    // Maildir 属主 UID（默认 5000，宝塔共存机用 150）
	vmailGID     int    // Maildir 属组 GID
}

// MaildirBase 返回 Maildir 基础路径
func (m *Manager) MaildirBase() string {
	return m.maildirBase
}

// ActiveCount 返回本节点当前活跃邮箱账号数（Dovecot users.conf 中的有效行数）。
// 供心跳上报为 load，供 mgmt 周期性校准 mail_servers.current_load。
func (m *Manager) ActiveCount() int {
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
		maildirBase:  maildirBase,
		usersFile:    "/etc/dovecot/users.conf",
		vmailboxFile: "/etc/postfix/vmailbox",
		vmailUID:     vmailUID,
		vmailGID:     vmailGID,
	}
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
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid email address: %s", email)
	}
	localPart := parts[0]
	domain := parts[1]

	info := &MailboxInfo{
		EmailAddress: email,
		Domain:       domain,
		LocalPart:    localPart,
		MaildirPath:  filepath.Join(m.maildirBase, domain, localPart),
	}

	// 幂等：邮箱已存在则直接返回，不重复追加 users.conf / vmailbox
	if m.mailboxExists(email) {
		return info, nil
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
	domainDir := filepath.Join(m.maildirBase, domain)
	filepath.Walk(domainDir, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Chown(p, m.vmailUID, m.vmailGID)
	})

	// 2. 添加到 Dovecot users.conf
	// 格式: email:{SHA512-CRYPT}password::::::
	entry := fmt.Sprintf("%s:{PLAIN}%s::::::\n", email, password)
	if err := m.appendToFile(m.usersFile, entry); err != nil {
		return nil, fmt.Errorf("add dovecot user: %w", err)
	}

	// 3. 添加到 Postfix virtual mailbox maps
	vmailEntry := fmt.Sprintf("%s %s/\n", email, filepath.Join(domain, localPart))
	if err := m.appendToFile(m.vmailboxFile, vmailEntry); err != nil {
		return nil, fmt.Errorf("add postfix vmailbox: %w", err)
	}

	// 4. 重新生成 postfix 哈希表并重载
	exec.Command("postmap", m.vmailboxFile).Run()
	exec.Command("postfix", "reload").Run()

	return info, nil
}

// UpdatePassword rewrites a user's password line in Dovecot users.conf.
// It reads the file, replaces the matching line, writes atomically (.tmp → rename),
// and runs doveadm reload.
func (m *Manager) UpdatePassword(email, newPassword string) error {
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

	// Atomic write: write to .tmp first, then rename (atomic on same filesystem).
	tmpPath := m.usersFile + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		return fmt.Errorf("write tmp users.conf: %w", err)
	}
	if err := os.Rename(tmpPath, m.usersFile); err != nil {
		return fmt.Errorf("rename users.conf: %w", err)
	}

	// Reload Dovecot so the new password takes effect immediately.
	exec.Command("doveadm", "reload").Run()

	return nil
}

// Delete 安全删除邮箱（软删除：Rename 到 .trash/ 而非 rm -rf）。
// 协议见 forwarding-design.md §9。
//
// 调用方如需完整的"摘除 Postfix/Dovecot → 等待转发排空 → 软删除"协议，
// 请使用 forward.Lifecycle.MoveToTrash 代替本方法。
func (m *Manager) Delete(email string) error {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid email: %s", email)
	}
	localPart := parts[0]
	domain := parts[1]

	maildirPath := filepath.Join(m.maildirBase, domain, localPart)

	// Verify the mailbox exists
	if _, err := os.Stat(maildirPath); os.IsNotExist(err) {
		return fmt.Errorf("mailbox not found: %s", email)
	}

	// Remove from Postfix & Dovecot configs
	if err := m.RemoveFromConfigs(email); err != nil {
		// Non-fatal: the critical step is the rename
		fmt.Printf("manager.Delete: remove configs warning: %v\n", err)
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
	// 1. Remove from Postfix virtual mailbox maps
	if err := m.removeLineFromFile(m.vmailboxFile, email); err != nil {
		return fmt.Errorf("postfix vmailbox: %w", err)
	}
	exec.Command("postmap", m.vmailboxFile).Run()
	exec.Command("postfix", "reload").Run()

	// 2. Remove from Dovecot users.conf
	if err := m.removeLineFromFile(m.usersFile, email); err != nil {
		return fmt.Errorf("dovecot users: %w", err)
	}

	return nil
}

// removeLineFromFile rewrites a file without lines containing the given substring.
func (m *Manager) removeLineFromFile(path, substr string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var kept []string
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.Contains(line, substr) {
			kept = append(kept, line)
		}
	}

	return os.WriteFile(path, []byte(strings.Join(kept, "\n")), 0644)
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
