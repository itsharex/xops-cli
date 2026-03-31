package sftp

import (
	"fmt"

	"github.com/pkg/sftp"
	"github.com/wentf9/xops-cli/pkg/ssh"
)

type Option func(*Client)

func WithConcurrentFiles(con int) Option {
	return func(c *Client) {
		if con > 0 {
			c.config.ConcurrentFiles = con
		}
	}
}

func WithThreadsPerFile(t int) Option {
	return func(c *Client) {
		if t > 0 {
			c.config.ThreadsPerFile = t
		}
	}
}

func WithChunkSize(size int) Option {
	return func(c *Client) {
		if size > 0 {
			c.config.ChunkSize = int64(size)
		}
	}
}

func WithResume(enable bool) Option {
	return func(c *Client) {
		c.config.EnableResume = enable
	}
}

func WithResumeMinSize(size int64) Option {
	return func(c *Client) {
		if size > 0 {
			c.config.ResumeMinSize = size
		}
	}
}

func WithForce(force bool) Option {
	return func(c *Client) {
		c.config.Force = force
	}
}

func WithNoOverwrite(noOverwrite bool) Option {
	return func(c *Client) {
		c.config.NoOverwrite = noOverwrite
	}
}

// Client 包装了 sftp.Client，并持有底层的 ssh 连接引用
type Client struct {
	sftpClient *sftp.Client
	sshClient  *ssh.Client
	config     TransferConfig
}

// New 基于现有的 SSH 连接创建一个 SFTP 客户端
// 这里复用了 pkg/ssh 中已经建立好的连接 (包括跳板机隧道)
func NewClient(sshCli *ssh.Client, opts ...Option) (*Client, error) {
	sftpCli := &Client{
		sshClient: sshCli,
		config:    DefaultConfig(),
	}
	for _, opt := range opts {
		opt(sftpCli)
	}

	client, err := sftp.NewClient(
		sshCli.SSHClient(),
		sftp.MaxConcurrentRequestsPerFile(sftpCli.config.ThreadsPerFile),
		sftp.MaxPacket(32*1024),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create sftp subsystem: %w", err)
	}
	sftpCli.sftpClient = client

	return sftpCli, nil
}

// SFTPClient 返回底层的 *sftp.Client 对象，
// 允许调用者执行 rename, chmod, stat, symlink 等高级操作。
func (c *Client) SFTPClient() *sftp.Client {
	return c.sftpClient
}

// Config 返回当前传输配置
func (c *Client) Config() TransferConfig {
	return c.config
}

// SetForce 动态设置强制覆盖标志
func (c *Client) SetForce(force bool) {
	c.config.Force = force
}

// Close 关闭 SFTP 会话 (注意：这不会关闭底层的 SSH 连接，除非你希望这样)
func (c *Client) Close() error {
	return c.sftpClient.Close()
}

// Cwd 获取远程当前工作目录
func (c *Client) Cwd() (string, error) {
	return c.sftpClient.Getwd()
}

// JoinPath 辅助函数：处理远程路径拼接 (SFTP 协议强制使用 forward slash)
func (c *Client) JoinPath(elem ...string) string {
	return c.sftpClient.Join(elem...)
}
