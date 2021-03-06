package e2etests

import (
	"testing"

	"github.com/pinpt/ripsrc/ripsrc"
	"github.com/stretchr/testify/assert"
)

func TestMultipleBranches1(t *testing.T) {
	test := NewTest(t, "multiple_branches")
	got := test.Run(&ripsrc.RipOpts{AllBranches: true})

	if len(got) != 3 {
		t.Fatal("expecting changes for 3 commits")
	}

	assert := assert.New(t)
	c := got[0].Commit
	assert.Equal("6405a003b50894ad5bcfb0252eff8d4719ee15ef", c.SHA)
	assert.Equal([]string{"b", "master"}, c.Branches)

	c = got[1].Commit
	assert.Equal("8fd2147e148b5875c9765a7c1a3e245f8f6387b1", c.SHA)
	assert.Equal([]string{"master"}, c.Branches)

	c = got[2].Commit
	assert.Equal("c81b9e3799b0ee78b2db6455d7e723c32cebd6f3", c.SHA)
	assert.Equal([]string{"b"}, c.Branches)
}

func TestMultipleBranchesDisabled(t *testing.T) {
	test := NewTest(t, "multiple_branches_disabled")
	got := test.Run(nil)

	if len(got) != 1 {
		t.Fatalf("expecting changes for 1 commits, got\n%#+v", got)
	}

	assert := assert.New(t)
	c := got[0].Commit
	assert.Equal("3a82e44558db78d9e61661d3c85b0a79d23a1d48", c.SHA)
	if len(c.Branches) != 0 {
		t.Error("When AllBranches=false do not populate branches field.")
	}
}
