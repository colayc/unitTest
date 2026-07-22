package processcontrol

type HostCommand struct {
	Kind string `json:"kind"`
	Spec *Spec  `json:"spec,omitempty"`
}

type HostStatus struct {
	Kind         string `json:"kind"`
	PID          int    `json:"pid,omitempty"`
	ProcessGroup int    `json:"processGroup,omitempty"`
	ExitCode     int    `json:"exitCode,omitempty"`
	ErrorCode    string `json:"errorCode,omitempty"`
	Message      string `json:"message,omitempty"`
}

func StartCommand(spec Spec) HostCommand {
	return HostCommand{Kind: "start", Spec: &spec}
}

func StopCommand() HostCommand {
	return HostCommand{Kind: "stop"}
}
