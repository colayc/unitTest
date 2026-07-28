package protocolmodel

type WorkspaceSnapshot struct {
	Capabilities        Capabilities     `json:"capabilities"`
	Projects            []ProjectElement `json:"projects"`
	WorkspaceGeneration string           `json:"workspaceGeneration"`
	WorkspaceURI        string           `json:"workspaceUri"`
}

type Capabilities struct {
	CmakeBuild       bool `json:"cmakeBuild"`
	TargetList       bool `json:"targetList"`
	WorkspaceInspect bool `json:"workspaceInspect"`
}

type ProjectElement struct {
	BuildProfiles []BuildProfileElement `json:"buildProfiles"`
	ProjectID     string                `json:"projectId"`
	SourceURI     string                `json:"sourceUri"`
}

type BuildProfileElement struct {
	BuildProfileID string `json:"buildProfileId"`
	Name           string `json:"name"`
}
