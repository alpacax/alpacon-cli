package config

import "time"

// retryConfigRename bounds transient sharing failures while the writer lock is held.
func retryConfigRename(rename func() error, retryable func(error) bool) error {
	for attempt := 0; ; attempt++ {
		err := rename()
		if err == nil || attempt == 5 || !retryable(err) {
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}
}
