package testgenrender

import (
	"bytes"
	"math/rand"
	"os"
	"strings"
	"testing"

	"unit-test-ide.local/test-service/internal/managedtest"
	analysis "unit-test-ide.local/test-service/internal/testgenanalysis"
	assert "unit-test-ide.local/test-service/internal/testgenassert"
	solver "unit-test-ide.local/test-service/internal/testgensolver"
)

func managedRequest(lang Language) ManagedRenderInput {
	r := request(lang)
	r.Target.TestPath = "tests/generated/src/choose." + string(lang) + "_test." + string(lang)
	return ManagedRenderInput{
		Program: r.Program, ProjectID: "project", SourceRelativePath: "src/choose." + string(lang),
		SourceFileID: strings.Repeat("d", 32), Framework: map[Language]string{LanguageC: "unity", LanguageCPP: "cpputest"}[lang],
		GeneratorVersion: "1", Language: lang, HeaderPath: r.HeaderPath, Target: r.Target,
		Functions: []ManagedFunctionInput{{FunctionID: strings.Repeat("b", 32), SymbolID: idB, Cases: r.Cases}},
	}
}

func TestManagedGoldenFiles(t *testing.T) {
	for _, lang := range []Language{LanguageC, LanguageCPP} {
		got, err := RenderManagedFile(managedRequest(lang))
		if err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile("testdata/golden/managed/" + string(lang) + ".txt")
		if err != nil {
			t.Fatalf("golden missing: %v\ngot:\n%s", err, got.Content)
		}
		if !bytes.Equal(got.Content, want) {
			t.Fatalf("%s mismatch\ngot:\n%s\nwant:\n%s", lang, got.Content, want)
		}
	}
}

func TestRenderManagedFileDeterministicAndParseable(t *testing.T) {
	for _, lang := range []Language{LanguageC, LanguageCPP} {
		in := managedRequest(lang)
		first, err := RenderManagedFile(in)
		if err != nil {
			t.Fatal(err)
		}
		if first.Path != "tests/generated/src/choose."+string(lang)+"_test."+map[Language]string{LanguageC: "c", LanguageCPP: "cpp"}[lang] {
			t.Fatalf("path: %s", first.Path)
		}
		doc, err := managedtest.ParseDocument(first.Content, int64(len(first.Content)), 10)
		if err != nil || len(doc.Blocks) != 1 {
			t.Fatalf("unparseable render: %v, %d", err, len(doc.Blocks))
		}
		if !bytes.Contains(doc.Blocks[0].Body, []byte("// Arrange\n")) || !bytes.Contains(doc.Blocks[0].Body, []byte("// Act\n")) || !bytes.Contains(doc.Blocks[0].Body, []byte("// Assert\n")) {
			t.Fatalf("missing AAA body: %s", doc.Blocks[0].Body)
		}
		if !bytes.Contains(doc.Blocks[0].Body, []byte("returns_7")) {
			t.Fatalf("case name does not describe return behavior: %s", doc.Blocks[0].Body)
		}
		second, err := RenderManagedFile(in)
		if err != nil || !bytes.Equal(first.Content, second.Content) {
			t.Fatalf("not byte-stable: %v", err)
		}
	}
}

func TestRenderManagedFileRejectsAliasAndDuplicateIDs(t *testing.T) {
	in := managedRequest(LanguageCPP)
	for _, path := range []string{"src/../choose.cpp", "src\\choose.cpp", "src/%63hoose.cpp", "C:/choose.cpp"} {
		in.SourceRelativePath = path
		if _, err := RenderManagedFile(in); err == nil {
			t.Fatalf("accepted path %q", path)
		}
	}
	in = managedRequest(LanguageCPP)
	in.Functions = append(in.Functions, in.Functions[0])
	if _, err := RenderManagedFile(in); err == nil {
		t.Fatal("duplicate function accepted")
	}
}

func TestManagedPathsDisambiguateSourceExtensionsAndCraftedStems(t *testing.T) {
	paths := []string{"src/choose.cpp", "src/choose.cc", "src/choose.cxx", "src/choose.cpp_test.cpp"}
	seen := map[string]bool{}
	for _, source := range paths {
		in := managedRequest(LanguageCPP)
		in.SourceRelativePath = source
		in.Target.TestPath = ""
		file, err := RenderManagedFile(in)
		if err != nil {
			t.Fatal(err)
		}
		if seen[file.Path] {
			t.Fatalf("different source paths collided at %q", file.Path)
		}
		seen[file.Path] = true
		if file.Path != "tests/generated/"+source+"_test.cpp" {
			t.Fatalf("non-injective mapping for %q: %q", source, file.Path)
		}
	}
}

func TestManagedCppCaseAdditionDoesNotChangeUnmanagedScaffold(t *testing.T) {
	in := managedRequest(LanguageCPP)
	first, err := RenderManagedFile(in)
	if err != nil {
		t.Fatal(err)
	}
	more := in.Functions[0].Cases[0]
	more.Vector.ID = idA
	more.Observation.CandidateID = idA
	in.Functions[0].Cases = append(in.Functions[0].Cases, more)
	proof := in.Program.Functions[0].OracleProofs[0]
	proof.CandidateID = idA
	in.Program.Functions[0].OracleProofs = append(in.Program.Functions[0].OracleProofs, proof)
	second, err := RenderManagedFile(in)
	if err != nil {
		t.Fatal(err)
	}
	outside := func(raw []byte) string {
		doc, err := managedtest.ParseDocument(raw, int64(len(raw)), 10)
		if err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		cursor := 0
		for _, block := range doc.Blocks {
			b.Write(doc.Bytes[cursor:block.StartByte])
			cursor = block.EndByte
		}
		b.Write(doc.Bytes[cursor:])
		return b.String()
	}
	if outside(first.Content) != outside(second.Content) {
		t.Fatalf("adding C++ case changed unmanaged bytes\nbefore:%q\nafter:%q", outside(first.Content), outside(second.Content))
	}
}

func TestRenderManagedFileOrderIndependent(t *testing.T) {
	in := managedRequest(LanguageCPP)
	// Two independently proven cases in one function must render in stable ID order.
	in.Functions[0].Cases = append(in.Functions[0].Cases, in.Functions[0].Cases[0])
	in.Functions[0].Cases[1].Vector.ID = idA
	in.Functions[0].Cases[1].Observation.CandidateID = idA
	in.Program.Functions[0].OracleProofs = append(in.Program.Functions[0].OracleProofs, in.Program.Functions[0].OracleProofs[0])
	in.Program.Functions[0].OracleProofs[1].CandidateID = idA
	first, err := RenderManagedFile(in)
	if err != nil {
		t.Fatal(err)
	}
	for seed := int64(0); seed < 10; seed++ {
		other := in
		other.Functions = append([]ManagedFunctionInput(nil), in.Functions...)
		other.Functions[0].Cases = append([]Case(nil), in.Functions[0].Cases...)
		rand.New(rand.NewSource(seed)).Shuffle(len(other.Functions[0].Cases), func(i, j int) {
			other.Functions[0].Cases[i], other.Functions[0].Cases[j] = other.Functions[0].Cases[j], other.Functions[0].Cases[i]
		})
		got, err := RenderManagedFile(other)
		if err != nil || !bytes.Equal(got.Content, first.Content) {
			t.Fatalf("seed %d changed bytes: %v", seed, err)
		}
	}
}

func TestRenderManagedFileManyFunctionsShareOneScaffold(t *testing.T) {
	for _, lang := range []Language{LanguageC, LanguageCPP} {
		in := managedRequest(lang)
		secondFn := in.Program.Functions[0]
		secondFn.SymbolID = idA
		secondFn.Name = "select"
		secondFn.OracleProofs = append([]analysis.OracleProof(nil), secondFn.OracleProofs...)
		secondFn.OracleProofs[0].TargetDigest = idA
		in.Program.Functions = append(in.Program.Functions, secondFn)
		secondCase := in.Functions[0].Cases[0]
		secondCase.Observation.SymbolID = idA
		secondCase.Observation.Evidence = append([]assert.Evidence(nil), secondCase.Observation.Evidence...)
		secondCase.Observation.Evidence[0].TargetDigest = idA
		in.Functions = append(in.Functions, ManagedFunctionInput{FunctionID: strings.Repeat("a", 32), SymbolID: idA, Cases: []Case{secondCase}})
		first, err := RenderManagedFile(in)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Count(first.Content, []byte("#include \"include/choose.h\"")) != 1 {
			t.Fatalf("duplicate scaffold/group: %s", first.Content)
		}
		if lang == LanguageCPP && bytes.Count(first.Content, []byte("TEST_GROUP(")) != 2 || lang == LanguageC && (bytes.Count(first.Content, []byte("void test_")) != 2 || bytes.Count(first.Content, []byte("int main(void)")) != 1) {
			t.Fatalf("duplicate or missing group/runner: %s", first.Content)
		}
		doc, err := managedtest.ParseDocument(first.Content, int64(len(first.Content)), 10)
		if err != nil || len(doc.Blocks) != 2 || doc.Blocks[0].FunctionID != strings.Repeat("a", 32) {
			t.Fatalf("unstable function ordering: %+v %v", doc.Blocks, err)
		}
		in.Functions[0], in.Functions[1] = in.Functions[1], in.Functions[0]
		again, err := RenderManagedFile(in)
		if err != nil || !bytes.Equal(first.Content, again.Content) {
			t.Fatalf("function input order changed output: %v", err)
		}
	}
}

func TestRenderManagedFileRejectsNoncanonicalNumbers(t *testing.T) {
	in := managedRequest(LanguageCPP)
	in.Functions[0].Cases[0].Observation.Evidence[0].Expected.Integer = "+7"
	in.Program.Functions[0].OracleProofs[0].ExpectedDigest = assert.ValueDigest(in.Functions[0].Cases[0].Observation.Evidence[0].Expected)
	if _, err := RenderManagedFile(in); err == nil {
		t.Fatal("noncanonical numeric assertion accepted")
	}
}

func TestRenderManagedFileBoundsLongNames(t *testing.T) {
	in := managedRequest(LanguageCPP)
	in.Program.Functions[0].Name = strings.Repeat("F", 128)
	file, err := RenderManagedFile(in)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := managedtest.ParseDocument(file.Content, int64(len(file.Content)), 2)
	if err != nil || len(doc.Blocks) != 1 {
		t.Fatalf("long-name render invalid: %v", err)
	}
	for _, line := range strings.Split(string(doc.Blocks[0].Body), "\n") {
		if strings.HasPrefix(line, "TEST_GROUP(") && len(line) > 128 {
			t.Fatalf("generated group name unbounded: %d", len(line))
		}
	}
}

func TestRenderManagedFileRejectsUnusableProvenance(t *testing.T) {
	in := managedRequest(LanguageC)
	in.GeneratorVersion = "version\nforged"
	if _, err := RenderManagedFile(in); err == nil {
		t.Fatal("control-bearing generator provenance accepted")
	}
}

func TestManagedNamesUseFullCaseIdentity(t *testing.T) {
	a := "utc_aaaaaaaaaaaaaaaa0000000000000000"
	b := "utc_aaaaaaaaaaaaaaaa1111111111111111"
	if managedName("choose", "returns_7", a) == managedName("choose", "returns_7", b) {
		t.Fatal("different case IDs collided in generated names")
	}
}

func TestManagedBehaviorDoesNotMislabelAggregateReturnAsOutputMutation(t *testing.T) {
	got := managedBehavior([]assert.Assertion{{Target: assert.TargetReturn, Expected: solver.Value{Kind: analysis.TypeArray}}})
	if got != "returns_value" {
		t.Fatalf("aggregate return mislabeled: %s", got)
	}
}
