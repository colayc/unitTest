package protocolmodel

type TargetList struct {
	ProjectID           string             `json:"projectId"`
	Targets             []TargetListSchema `json:"targets"`
	WorkspaceGeneration string             `json:"workspaceGeneration"`
}

type TargetListSchema struct {
	Name     string `json:"name"`
	TargetID string `json:"targetId"`
}
