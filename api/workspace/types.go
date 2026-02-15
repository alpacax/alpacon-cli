package workspace

// Workspace represents a workspace entry from the JWT token's workspaces claim.
type Workspace struct {
	SchemaName string `json:"schema_name"`
	Auth0ID    string `json:"auth0_id"`
	Region     string `json:"region"`
}

// WorkspaceListEntry is used for displaying workspace information in a table.
type WorkspaceListEntry struct {
	Name    string
	Region  string
	Current string
}
