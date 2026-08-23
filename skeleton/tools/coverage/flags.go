package main

import "flag"

// newFlagSet builds the command line. It is separate so a test can construct
// the same set without going through main.
func newFlagSet(profiles *profileList) *flag.FlagSet {
	fs := flag.NewFlagSet("coverage", flag.ContinueOnError)
	fs.Var(profiles, "profile",
		"a coverage profile to include; repeat the flag for each test tier")
	return fs
}
