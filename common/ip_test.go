package common

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsIP(t *testing.T) {
	assert.True(t, IsIP("192.168.0.1"))
	assert.True(t, IsIP("::1"))
	assert.False(t, IsIP("not-an-ip"))
	assert.False(t, IsIP(""))
}

func TestParseIP(t *testing.T) {
	assert.NotNil(t, ParseIP("10.0.0.1"))
	assert.Nil(t, ParseIP("garbage"))
}

func TestIsPrivateIP(t *testing.T) {
	privates := []string{
		"127.0.0.1",      // loopback
		"169.254.0.1",    // link-local unicast
		"10.1.2.3",       // 10/8
		"172.16.5.5",     // 172.16/12
		"192.168.1.100",  // 192.168/16
	}
	for _, ip := range privates {
		assert.True(t, IsPrivateIP(net.ParseIP(ip)), "%s should be private", ip)
	}

	publics := []string{"8.8.8.8", "1.1.1.1", "172.32.0.1"}
	for _, ip := range publics {
		assert.False(t, IsPrivateIP(net.ParseIP(ip)), "%s should be public", ip)
	}
}

func TestIsIpInCIDRList(t *testing.T) {
	ip := net.ParseIP("192.168.1.50")
	require.NotNil(t, ip)

	assert.True(t, IsIpInCIDRList(ip, []string{"192.168.1.0/24"}))
	assert.False(t, IsIpInCIDRList(ip, []string{"10.0.0.0/8"}))

	// single-IP entry (not CIDR) matched exactly
	assert.True(t, IsIpInCIDRList(ip, []string{"192.168.1.50"}))
	assert.False(t, IsIpInCIDRList(ip, []string{"192.168.1.51"}))

	// invalid entries are skipped without error
	assert.True(t, IsIpInCIDRList(ip, []string{"not-a-cidr", "192.168.1.0/24"}))
	assert.False(t, IsIpInCIDRList(ip, []string{"not-a-cidr"}))
	assert.False(t, IsIpInCIDRList(ip, nil))
}
