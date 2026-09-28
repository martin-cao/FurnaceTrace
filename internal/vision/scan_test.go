package vision

import "testing"

func TestContinuousScanLatchesAndRearms(t *testing.T) {
	c := Continuous{}
	steps := []struct {
		codes []string
		want  string
	}{
		{[]string{"D100001"}, ""}, {[]string{"D100001"}, "D100001"},
		{[]string{"D100001"}, ""}, {nil, ""},
		{[]string{"D100001"}, ""}, {[]string{"D100001"}, ""},
		{[]string{"D200001", "D100001"}, ""},
		{[]string{"D200001"}, ""}, {[]string{"D200001"}, "D200001"},
		{nil, ""}, {nil, ""}, {nil, ""},
		{[]string{"D200001"}, ""}, {[]string{"D200001"}, "D200001"},
	}
	for i, step := range steps {
		if got := c.Observe(step.codes); got != step.want {
			t.Fatalf("step %d: got %q, want %q", i, got, step.want)
		}
	}
}

func TestOnlyTwoConsecutiveSingleCodesAreAccepted(t *testing.T) {
	d := Stability{}
	if d.Observe([]string{"TR04ABC0001"}) != "" {
		t.Fatal("one frame accepted")
	}
	if d.Observe([]string{"TR04ABC0001", "TR04ABC0002"}) != "" {
		t.Fatal("ambiguous frame accepted")
	}
	if d.Observe([]string{"TR04ABC0001"}) != "" {
		t.Fatal("ambiguous gap did not reset stability")
	}
	if got := d.Observe([]string{"TR04ABC0001"}); got != "TR04ABC0001" {
		t.Fatalf("stable code lost: %q", got)
	}
}
