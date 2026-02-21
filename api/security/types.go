package security

type CommandAclRequest struct {
	Token   string `json:"token"`
	Command string `json:"command"`
}

type CommandAclResponse struct {
	ID        string `json:"id"`
	Token     string `json:"token"`
	TokenName string `json:"token_name"`
	Command   string `json:"command"`
}
