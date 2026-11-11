package client

import "github.com/alpacax/alpacon-cli/pkg/localsignal"

func (ac *AlpaconClient) LocalSignalOrigin() localsignal.ConsoleOrigin {
	if ac == nil {
		return localsignal.ConsoleOrigin{}
	}
	return localsignal.ConsoleOrigin{WorkspaceURL: ac.BaseURL, BaseDomain: ac.BaseDomain}
}

func (e *apiError) LocalSignalOrigin() localsignal.ConsoleOrigin { return e.origin }
