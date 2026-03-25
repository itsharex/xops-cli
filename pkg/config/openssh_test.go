package config

import (
	"github.com/kevinburke/ssh_config"
	"testing"
)

func TestOpenSSHParser(t *testing.T) {
	// try to figure out what it returns
	val := ssh_config.Get("unknown_host_123", "HostName")
	t.Logf("HostName for unknown: %s", val)

	val = ssh_config.Get("unknown_host_123", "IdentityFile")
	t.Logf("IdentityFile for unknown: %s", val)

	val = ssh_config.Get("unknown_host_123", "User")
	t.Logf("User for unknown: %s", val)

	val = ssh_config.Get("unknown_host_123", "Port")
	t.Logf("Port for unknown: %s", val)
}
