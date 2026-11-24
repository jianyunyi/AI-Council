package wskind

import (
	"context"
	"errors"
	"testing"
)

type fakeLookup map[string]string

func (f fakeLookup) WorkspaceKind(_ context.Context, id string) (string, error) {
	k, ok := f[id]
	if !ok {
		return "", errors.New("not found")
	}
	return k, nil
}

func TestCheckExecutable(t *testing.T) {
	cases := []struct {
		kind    string
		wantErr error
	}{
		{"", nil},
		{Standard, nil},
		{ModelPrivate, nil},
		{GroupShared, ErrNotExecutable},
		{"bogus", ErrUnknownKind},
	}
	for _, c := range cases {
		err := CheckExecutable(c.kind)
		if c.wantErr == nil && err != nil {
			t.Fatalf("kind %q: unexpected err %v", c.kind, err)
		}
		if c.wantErr != nil && !errors.Is(err, c.wantErr) {
			t.Fatalf("kind %q: want %v got %v", c.kind, c.wantErr, err)
		}
	}
}

func TestGuardExecution(t *testing.T) {
	l := fakeLookup{"ws_std": Standard, "ws_grp": GroupShared, "ws_mdl": ModelPrivate}
	ctx := context.Background()
	if err := GuardExecution(ctx, l, "ws_std"); err != nil {
		t.Fatal(err)
	}
	if err := GuardExecution(ctx, l, "ws_mdl"); err != nil {
		t.Fatal(err)
	}
	if err := GuardExecution(ctx, l, "ws_grp"); !errors.Is(err, ErrNotExecutable) {
		t.Fatalf("group_shared must be rejected, got %v", err)
	}
	if err := GuardExecution(ctx, l, "missing"); err == nil {
		t.Fatal("missing workspace must fail closed")
	}
}
