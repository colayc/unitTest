package managedtest

import "testing"

func TestReviewPreimageSetDigestOrdersAndFramesPathAndBytes(t *testing.T) {
	a := ReviewCandidate{CandidateID: "utc_11111111111111111111111111111111", TestRelativePath: "tests/generated/src/a_test.cpp", CurrentBytes: []byte("a"), GeneratedBytes: []byte("xy")}
	b := ReviewCandidate{CandidateID: "utc_22222222222222222222222222222222", TestRelativePath: "tests/generated/src/b_test.cpp", CurrentBytes: []byte("bc"), GeneratedBytes: []byte("z")}
	current, generated := ReviewPreimageSetDigests([]ReviewCandidate{a, b})
	reversedCurrent, reversedGenerated := ReviewPreimageSetDigests([]ReviewCandidate{b, a})
	if current != reversedCurrent || generated != reversedGenerated {
		t.Fatal("input ordering changed canonical set digest")
	}
	segmented := []ReviewCandidate{a, b}
	segmented[0].CurrentBytes = []byte("ab")
	segmented[1].CurrentBytes = []byte("c")
	otherCurrent, otherGenerated := ReviewPreimageSetDigests(segmented)
	if otherCurrent == current || otherGenerated != generated {
		t.Fatal("byte-length framing failed to distinguish segment boundaries")
	}
	repathed := []ReviewCandidate{a, b}
	repathed[0].TestRelativePath = "tests/generated/src/other_test.cpp"
	pathCurrent, pathGenerated := ReviewPreimageSetDigests(repathed)
	if pathCurrent == current || pathGenerated == generated {
		t.Fatal("test path was not committed")
	}
}
