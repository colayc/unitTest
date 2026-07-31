package processcontrol

type HostCommand struct {
	Kind string `json:"kind"`
	Spec *Spec  `json:"spec,omitempty"`
}

type HostStatus struct {
	Kind                string            `json:"kind"`
	PID                 int               `json:"pid,omitempty"`
	ProcessGroup        int               `json:"processGroup,omitempty"`
	TargetProcessGroups []int             `json:"targetProcessGroups,omitempty"`
	ExitCode            int               `json:"exitCode,omitempty"`
	ErrorCode           string            `json:"errorCode,omitempty"`
	Message             string            `json:"message,omitempty"`
	Source              string            `json:"source,omitempty"`
	Stream              Stream            `json:"stream,omitempty"`
	Data                []byte            `json:"data,omitempty"`
	Children            []HostChildResult `json:"children,omitempty"`
}

type HostChildResult struct {
	ID        string `json:"id"`
	ExitCode  int    `json:"exitCode"`
	TimedOut  bool   `json:"timedOut,omitempty"`
	ErrorCode string `json:"errorCode,omitempty"`
}

func StartCommand(spec Spec) HostCommand {
	return HostCommand{Kind: "start", Spec: &spec}
}

func StopCommand() HostCommand {
	return HostCommand{Kind: "stop"}
}
