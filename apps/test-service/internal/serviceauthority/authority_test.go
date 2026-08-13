package serviceauthority

import "testing"

func TestMintBindsRootAndRejectsZeroOrWrongAuthority(t *testing.T) {
	authority, err := Mint(`C:\service\coverage`)
	if err != nil {
		t.Fatal(err)
	}
	if err := authority.Verify(`C:\service\coverage`); err != nil {
		t.Fatal(err)
	}
	if err := authority.Verify(`C:\other\coverage`); err == nil {
		t.Fatal("authority accepted wrong root")
	}
	var zero Authority
	if err := zero.Verify(`C:\service\coverage`); err == nil {
		t.Fatal("zero authority accepted")
	}
}
