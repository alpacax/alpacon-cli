package client

import "github.com/alpacax/alpacon-cli/config"

// SaveActiveWorkSession makes uuid the active work session of this client's workspace, unless another
// login has replaced the one the client was built with (config.ErrLoginChanged).
func (ac *AlpaconClient) SaveActiveWorkSession(uuid string) error {
	return config.SetActiveWorkSessionFor(ac.login, ac.WorkspaceName, uuid)
}
