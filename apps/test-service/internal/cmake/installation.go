package cmake

const (
	SourceOverride = "override"
	SourceBundle   = "bundle"
	SourceDev      = "dev"
)

type Installation struct {
	Executable  string
	Version     string
	Source      string
	Identity    string
	LicensePath string
}

type ResolverConfig struct {
	BundleRoot    string
	Override      string
	DevExecutable string
	Platform      string
	Architecture  string
}
