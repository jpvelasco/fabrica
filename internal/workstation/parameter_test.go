package workstation

import "testing"

func TestSessionPasswordParameterARN(t *testing.T) {
	got, err := SessionPasswordParameterARN("us-west-2", "575108928122")
	if err != nil {
		t.Fatal(err)
	}
	const want = "arn:aws:ssm:us-west-2:575108928122:parameter/fabrica/workstation/dcv-password"
	if got != want {
		t.Fatalf("ARN = %q, want %q", got, want)
	}
	if _, err := SessionPasswordParameterARN("", "123"); err == nil {
		t.Fatal("expected error for empty region")
	}
	if _, err := SessionPasswordParameterARN("us-west-2", ""); err == nil {
		t.Fatal("expected error for empty account")
	}
}
