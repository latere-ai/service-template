// Package store holds the database pool and the migration runner.
//
// Two rules shape it. Migrations are forward only and apply as a separate step
// before the new code starts, because two replicas starting at once would race
// and a start-up migration ties a schema change to rollout timing. Every query
// takes a context and every pool acquisition is bounded, because a query with
// no deadline holds a pool slot until the server answers and a slow dependency
// then exhausts the pool.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Migration is one forward migration file.
type Migration struct {
	// Version is the numeric prefix of the file name. It orders the sequence
	// and is the primary key of the tracking table.
	Version int64
	// Name is the descriptive part of the file name, without the version and
	// without the suffix.
	Name string
	// Path is the file name inside the migrations directory.
	Path string
	// SQL is the file content, applied verbatim in one transaction.
	SQL string
	// Digest is the hex SHA-256 of the file bytes. It is recorded when the
	// migration applies, so a later edit of an applied file is detected.
	Digest string
}

// migrationName matches a migration file name: a numeric version, a lowercase
// descriptive name, and the direction. The direction is explicit so an
// unlabelled file is a load error rather than a migration applied by accident.
var migrationName = regexp.MustCompile(`^([0-9]{4,})_([a-z0-9]+(?:_[a-z0-9]+)*)\.(up|down)\.sql$`)

// Load reads the forward migrations from fsys, ordered by version.
//
// It fails on anything ambiguous: an SQL file whose name does not carry a
// version and a direction, two files with the same version, or a down file
// with no matching up file. A migration set that cannot be read exactly is
// worse than no migration set, because the runner would apply a subset.
func Load(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read the migrations directory: %w", err)
	}

	var problems []error
	ups := make(map[int64]Migration)
	downs := make(map[int64]string)

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		m := migrationName.FindStringSubmatch(name)
		if m == nil {
			problems = append(problems, fmt.Errorf(
				"%s: a migration file is named <version>_<name>.up.sql or <version>_<name>.down.sql", name))
			continue
		}
		version, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: version %q is not a number: %w", name, m[1], err))
			continue
		}
		if m[3] == "down" {
			if prev, ok := downs[version]; ok {
				problems = append(problems, fmt.Errorf("%s: version %d is already used by %s", name, version, prev))
				continue
			}
			downs[version] = name
			continue
		}
		if prev, ok := ups[version]; ok {
			problems = append(problems, fmt.Errorf("%s: version %d is already used by %s", name, version, prev.Path))
			continue
		}
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", name, err))
			continue
		}
		if strings.TrimSpace(string(data)) == "" {
			problems = append(problems, fmt.Errorf("%s: the file is empty", name))
			continue
		}
		sum := sha256.Sum256(data)
		ups[version] = Migration{
			Version: version,
			Name:    m[2],
			Path:    name,
			SQL:     string(data),
			Digest:  hex.EncodeToString(sum[:]),
		}
	}

	for version, name := range downs {
		if _, ok := ups[version]; !ok {
			problems = append(problems, fmt.Errorf("%s: there is no up migration for version %d", name, version))
		}
	}
	if err := errors.Join(problems...); err != nil {
		return nil, err
	}

	out := make([]Migration, 0, len(ups))
	for _, m := range ups {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}
