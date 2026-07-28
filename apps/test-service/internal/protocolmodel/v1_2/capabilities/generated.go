package protocolmodelv12capabilities

type CapabilitiesV12 struct {
	CmakeBuild       bool `json:"cmakeBuild"`
	TargetList       bool `json:"targetList"`
	WorkspaceInspect bool `json:"workspaceInspect"`
}
