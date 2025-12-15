// Package tunnel provides optimized tunnel utilities for WebSocket+smux connections.
package tunnel

import (
	"time"

	"github.com/xtaci/smux"
)

// GetSmuxConfig returns a ready-to-use smux configuration.
func GetSmuxConfig() *smux.Config {
	config := smux.DefaultConfig()
	config.KeepAliveInterval = 10 * time.Second // connection health check
	config.KeepAliveTimeout = 30 * time.Second  // abnormal connection detection
	config.MaxFrameSize = 32768                 // 32KB
	config.MaxReceiveBuffer = 4194304           // 4MB
	config.MaxStreamBuffer = 65536              // 64KB per stream
	return config
}
