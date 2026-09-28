package coveragedomain

// These observations are parser-side evidence, not fields of the v1 report.
// A zero endpoint or column means that the collector did not establish it.
// File is a collector path until a later source-binding stage replaces it
// with a verified workspace-relative URI; it must not be serialized publicly.
type SourceLocation struct {
	Line, Column int64
}

type LineObservation struct {
	Line, Count int64
}

type BranchObservation struct {
	Line, Column int64
	Ordinal      int64
	HasOrdinal   bool
	Count        int64
}

type FunctionObservation struct {
	QualifiedName        string
	LinkageName          string
	SignatureDigest      string
	File                 string
	Start, End           SourceLocation
	ExecutionCount       int64
	Lines                []LineObservation
	Branches             []BranchObservation
	InstantiationOrdinal int64
}

func (value FunctionObservation) HasExactRange() bool {
	start, end := value.Start, value.End
	if start.Line < 1 || start.Column < 1 || end.Line < 1 || end.Column < 1 ||
		start.Line > MaxSafeInteger || start.Column > MaxSafeInteger ||
		end.Line > MaxSafeInteger || end.Column > MaxSafeInteger {
		return false
	}
	return end.Line > start.Line || end.Line == start.Line && end.Column >= start.Column
}
