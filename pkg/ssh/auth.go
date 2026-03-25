package ssh

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/term"
)

// AuthMethod 定义获取 SSH 认证方法的接口
type AuthMethod interface {
	GetMethod() (ssh.AuthMethod, error)
}

// PasswordAuth 实现密码认证
type PasswordAuth struct {
	Password string
}

func (p *PasswordAuth) GetMethod() (ssh.AuthMethod, error) {
	return ssh.Password(p.Password), nil
}

// KeyAuth 实现私钥认证
type KeyAuth struct {
	Path       string
	Passphrase string
}

func (k *KeyAuth) GetMethod() (ssh.AuthMethod, error) {
	keyData, err := os.ReadFile(k.Path)
	if err != nil {
		return nil, err
	}
	var signer ssh.Signer
	if k.Passphrase != "" {
		signer, _ = ssh.ParsePrivateKeyWithPassphrase(keyData, []byte(k.Passphrase))
	} else {
		signer, _ = ssh.ParsePrivateKey(keyData)
	}
	return ssh.PublicKeys(signer), nil
}

// BuildAutoAuthMethods 生成一个包含多种回退机制的 AuthMethod 链
func BuildAutoAuthMethods(user, host, keyPath string) ([]ssh.AuthMethod, func()) {
	var methods []ssh.AuthMethod
	var cleanup func()

	// 1. SSH Agent
	if socket := os.Getenv("SSH_AUTH_SOCK"); socket != "" {
		if conn, err := net.Dial("unix", socket); err == nil {
			agentClient := agent.NewClient(conn)
			methods = append(methods, ssh.PublicKeysCallback(agentClient.Signers))
			cleanup = func() { _ = conn.Close() }
		}
	}

	// 2. Specific Key Path
	if keyPath != "" {
		keyAuth := &KeyAuth{Path: expandHomeDir(keyPath)}
		if m, err := keyAuth.GetMethod(); err == nil {
			methods = append(methods, m)
		}
	}

	// 3. Default Keys
	defaultKeys := []string{"~/.ssh/id_rsa", "~/.ssh/id_ed25519", "~/.ssh/id_ecdsa", "~/.ssh/id_dsa"}
	for _, p := range defaultKeys {
		if expandHomeDir(p) == expandHomeDir(keyPath) {
			continue
		}
		if _, err := os.Stat(expandHomeDir(p)); err == nil {
			keyAuth := &KeyAuth{Path: expandHomeDir(p)}
			if m, err := keyAuth.GetMethod(); err == nil {
				methods = append(methods, m)
			}
		}
	}

	// 4. Password Fallback
	methods = append(methods, ssh.RetryableAuthMethod(ssh.PasswordCallback(func() (string, error) {
		fmt.Printf("%s@%s's password: ", user, host)
		password, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return "", err
		}
		return string(password), nil
	}), 3))

	return methods, cleanup
}
