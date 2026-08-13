package serviceauthority

import (
	"errors"
	"path/filepath"
	"strings"
)

// Authority is an opaque service-owned provenance token. Its fields are
// private; only trusted service packages should call Mint.
type Authority struct {
	root  string
	token *token
}

type token struct{}

func Mint(root string) (Authority, error) {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return Authority{}, errors.New("invalid service authority root")
	}
	return Authority{root: root, token: &token{}}, nil
}

func (authority Authority) Verify(root string) error {
	if authority.token == nil || authority.root == "" || !pathWithin(authority.root, root) {
		return errors.New("invalid service-owned provenance")
	}
	return nil
}

func pathWithin(root, child string) bool {
	root, child = filepath.Clean(root), filepath.Clean(child)
	if filepath.VolumeName(root) != "" && !equalFold(filepath.VolumeName(root), filepath.VolumeName(child)) {
		return false
	}
	rel, err := filepath.Rel(root, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func (authority Authority) Root() string { return authority.root }

// SameIssuer checks that two capabilities were issued by the same opaque
// token, independent of copied Authority value wrappers.
func (authority Authority) SameIssuer(other Authority) bool {
	return authority.token != nil && authority.token == other.token
}

func samePath(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if filepath.VolumeName(left) != "" {
		return equalFold(left, right)
	}
	return left == right
}

func equalFold(left, right string) bool {
	for i := range left {
		if i >= len(right) || lower(left[i]) != lower(right[i]) {
			return false
		}
	}
	return len(left) == len(right)
}

func lower(value byte) byte {
	if value >= 'A' && value <= 'Z' {
		return value + ('a' - 'A')
	}
	return value
}
