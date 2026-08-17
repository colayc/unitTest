package protocolmodelv12targetlist

type TargetList struct {
	BuildProfileID      string             `json:"buildProfileId"`
	ProjectID           string             `json:"projectId"`
	Targets             []TargetListSchema `json:"targets"`
	WorkspaceGeneration string             `json:"workspaceGeneration"`
}

type TargetListSchema struct {
	Name     string `json:"name"`
	TargetID string `json:"targetId"`
}
