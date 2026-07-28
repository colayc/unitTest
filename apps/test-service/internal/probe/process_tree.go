package probe

type processTree interface {
	Wait() (int, error)
	Terminate() error
}
