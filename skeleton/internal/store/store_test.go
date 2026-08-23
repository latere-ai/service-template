package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDefaultOptionsAreValid(t *testing.T) {
	if err := DefaultOptions().validate(); err != nil {
		t.Fatalf("the default options are invalid: %v", err)
	}
}

func TestOptionsRejectLimitsThatDisableThePool(t *testing.T) {
	cases := map[string]struct {
		change func(*Options)
		want   string
	}{
		"no connections":  {func(o *Options) { o.MaxConns = 0 }, "MaxConns"},
		"no lifetime":     {func(o *Options) { o.MaxConnLifetime = 0 }, "MaxConnLifetime"},
		"no idle time":    {func(o *Options) { o.MaxConnIdleTime = -time.Second }, "MaxConnIdleTime"},
		"no acquire wait": {func(o *Options) { o.AcquireTimeout = 0 }, "AcquireTimeout"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			o := DefaultOptions()
			tc.change(&o)
			err := o.validate()
			if err == nil {
				t.Fatalf("validate accepted %s", name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not name %s", err, tc.want)
			}
		})
	}
}

func TestPoolConfigAppliesTheTemplateLimits(t *testing.T) {
	cfg, err := poolConfig("postgres://user:pass@localhost:5432/app", DefaultOptions())
	if err != nil {
		t.Fatalf("poolConfig: %v", err)
	}
	opts := DefaultOptions()
	if cfg.MaxConns != opts.MaxConns {
		t.Errorf("MaxConns = %d, want %d", cfg.MaxConns, opts.MaxConns)
	}
	if cfg.MaxConnLifetime != opts.MaxConnLifetime {
		t.Errorf("MaxConnLifetime = %s, want %s", cfg.MaxConnLifetime, opts.MaxConnLifetime)
	}
	if cfg.MaxConnIdleTime != opts.MaxConnIdleTime {
		t.Errorf("MaxConnIdleTime = %s, want %s", cfg.MaxConnIdleTime, opts.MaxConnIdleTime)
	}
}

func TestPoolConfigKeepsWhatTheConnectionStringSets(t *testing.T) {
	dsn := "postgres://user:pass@localhost:5432/app?pool_max_conns=3&pool_max_conn_lifetime=90s"
	cfg, err := poolConfig(dsn, DefaultOptions())
	if err != nil {
		t.Fatalf("poolConfig: %v", err)
	}
	if cfg.MaxConns != 3 {
		t.Errorf("MaxConns = %d, want the 3 the connection string sets", cfg.MaxConns)
	}
	if cfg.MaxConnLifetime != 90*time.Second {
		t.Errorf("MaxConnLifetime = %s, want the 90s the connection string sets", cfg.MaxConnLifetime)
	}
	// A limit the string does not set still comes from the options.
	if cfg.MaxConnIdleTime != DefaultOptions().MaxConnIdleTime {
		t.Errorf("MaxConnIdleTime = %s, want the default", cfg.MaxConnIdleTime)
	}
}

func TestOpenRejectsBadInput(t *testing.T) {
	cases := map[string]struct {
		dsn  string
		opts Options
		want string
	}{
		"empty connection string": {"", DefaultOptions(), "connection string is empty"},
		"unparseable connection string": {
			"://nonsense", DefaultOptions(), "parse the database connection string",
		},
		"invalid options": {
			"postgres://user@localhost/app",
			Options{MaxConns: 0, MaxConnLifetime: time.Minute, MaxConnIdleTime: time.Minute, AcquireTimeout: time.Second},
			"MaxConns",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s, err := OpenWith(t.Context(), tc.dsn, tc.opts)
			if err == nil {
				s.Close()
				t.Fatalf("OpenWith accepted %s", name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestOpenUsesTheDefaultOptions(t *testing.T) {
	// Open reaches the same validation as OpenWith, which is observable
	// without a server through the connection string check.
	if _, err := Open(t.Context(), ""); err == nil {
		t.Fatal("Open accepted an empty connection string")
	}
}

func TestErrorRowCarriesTheAcquisitionFailure(t *testing.T) {
	want := errors.New("no free connection")
	var id int64
	if err := (errorRow{err: want}).Scan(&id); !errors.Is(err, want) {
		t.Fatalf("Scan returned %v, want %v", err, want)
	}
}
