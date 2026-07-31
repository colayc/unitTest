package cmake

const (
	SourceOverride = "override"
	SourceBundle   = "bundle"
	SourceDev      = "dev"
)

type Installation struct {
	Executable           string
	CTestExecutable      string
	Root                 string
	Version              string
	Source               string
	Identity             string
	LicensePath          string
	UnityRunnerGenerator ProductExecutable
}

type ResolverConfig struct {
	BundleRoot    string
	Override      string
	DevExecutable string
	Platform      string
	Architecture  string
}
