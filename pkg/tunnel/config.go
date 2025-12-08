// Package tunnel provides optimized tunnel utilities for WebSocket+smux connections.
package tunnel

import (
	"time"

	"github.com/xtaci/smux"
)

// SmuxConfig holds smux session configuration for performance tuning.
type SmuxConfig struct {
	KeepAliveInterval time.Duration
	KeepAliveTimeout  time.Duration
	MaxFrameSize      int
	MaxReceiveBuffer  int
	MaxStreamBuffer   int
}

// DefaultSmuxConfig returns optimized smux configuration.
// - KeepAliveInterval: 10s (connection health check)
// - KeepAliveTimeout: 30s (abnormal connection detection)
// - MaxFrameSize: 32KB (max single frame size)
// - MaxReceiveBuffer: 4MB (total session receive buffer)
// - MaxStreamBuffer: 64KB (per-stream buffer)
func DefaultSmuxConfig() *SmuxConfig {
	return &SmuxConfig{
		KeepAliveInterval: 10 * time.Second,
		KeepAliveTimeout:  30 * time.Second,
		MaxFrameSize:      32768,   // 32KB
		MaxReceiveBuffer:  4194304, // 4MB
		MaxStreamBuffer:   65536,   // 64KB per stream
	}
}

// ToSmux converts SmuxConfig to *smux.Config.
func (c *SmuxConfig) ToSmux() *smux.Config {
	config := smux.DefaultConfig()
	config.KeepAliveInterval = c.KeepAliveInterval
	config.KeepAliveTimeout = c.KeepAliveTimeout
	config.MaxFrameSize = c.MaxFrameSize
	config.MaxReceiveBuffer = c.MaxReceiveBuffer
	config.MaxStreamBuffer = c.MaxStreamBuffer
	return config
}

// GetSmuxConfig returns a ready-to-use smux configuration.
func GetSmuxConfig() *smux.Config {
	return DefaultSmuxConfig().ToSmux()
}
