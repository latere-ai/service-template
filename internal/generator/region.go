// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package generator

import (
	"fmt"
	"strings"
)

// The managed region markers are the exact lines the contract fixes. A merged
// file is rewritten between them and preserved outside them, so the marker
// text is part of the contract and not a formatting choice.
const (
	MarkerStart = "# >>> template: managed region, do not edit <<<"
	MarkerEnd   = "# >>> template: end managed region <<<"
)

// Region is the managed part of a merged file together with the consumer text
// around it.
type Region struct {
	// Before holds the lines above the start marker.
	Before []string
	// Body holds the lines between the markers, which the template owns.
	Body []string
	// After holds the lines below the end marker.
	After []string
}

// Content returns the body as the bytes the lock digests.
func (r Region) Content() []byte {
	if len(r.Body) == 0 {
		return nil
	}
	return []byte(strings.Join(r.Body, "\n") + "\n")
}

// isMarker reports whether a line is the given marker, ignoring trailing
// whitespace and a carriage return so a CRLF checkout still parses.
func isMarker(line, marker string) bool {
	return strings.TrimRight(line, " \t\r") == marker
}

// SplitRegion divides a merged file into the consumer text and the managed
// body. A file whose markers are missing, unbalanced, or out of order fails
// here, because a silently unmanaged region is how a fix stops propagating.
func SplitRegion(path string, data []byte) (Region, error) {
	lines := strings.Split(string(data), "\n")
	var starts, ends []int
	for i, l := range lines {
		switch {
		case isMarker(l, MarkerStart):
			starts = append(starts, i)
		case isMarker(l, MarkerEnd):
			ends = append(ends, i)
		}
	}
	switch {
	case len(starts) == 0 && len(ends) == 0:
		return Region{}, fmt.Errorf(
			"%s carries no managed region; add the marker lines\n  %s\n  %s", path, MarkerStart, MarkerEnd)
	case len(starts) == 0:
		return Region{}, fmt.Errorf("%s has an end marker with no start marker", path)
	case len(ends) == 0:
		return Region{}, fmt.Errorf("%s has a start marker with no end marker", path)
	case len(starts) > 1 || len(ends) > 1:
		return Region{}, fmt.Errorf(
			"%s carries %d start markers and %d end markers; the template owns exactly one region",
			path, len(starts), len(ends))
	case ends[0] < starts[0]:
		return Region{}, fmt.Errorf("%s has the end marker on line %d, above the start marker on line %d",
			path, ends[0]+1, starts[0]+1)
	}
	return Region{
		Before: lines[:starts[0]],
		Body:   lines[starts[0]+1 : ends[0]],
		After:  lines[ends[0]+1:],
	}, nil
}

// Splice rewrites the managed body of an existing merged file and returns the
// whole file. Every byte outside the markers survives unchanged.
func Splice(path string, disk []byte, body []string) ([]byte, error) {
	r, err := SplitRegion(path, disk)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(r.Before)+len(body)+len(r.After)+2)
	out = append(out, r.Before...)
	out = append(out, MarkerStart)
	out = append(out, body...)
	out = append(out, MarkerEnd)
	out = append(out, r.After...)
	return []byte(strings.Join(out, "\n")), nil
}

// StripRegion removes the managed region and its markers, which is what a sync
// does to a merged file the declaration no longer selects. The consumer text
// stays.
func StripRegion(path string, disk []byte) ([]byte, error) {
	r, err := SplitRegion(path, disk)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(r.Before)+len(r.After))
	out = append(out, r.Before...)
	out = append(out, r.After...)
	return []byte(strings.Join(out, "\n")), nil
}
