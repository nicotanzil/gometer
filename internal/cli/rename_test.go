package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRenamedCLIIdentity(t *testing.T) {
	var out bytes.Buffer
	if code := Run(context.Background(), []string{"version"}, &out, &out); code != 0 || out.String() != "GoMeter "+Version+"\n" {
		t.Fatalf("version: code=%d output=%q", code, out.String())
	}
	out.Reset()
	if code := Run(context.Background(), []string{"help"}, &out, &out); code != 0 || !strings.Contains(out.String(), "gometer run") || !strings.Contains(out.String(), "GoMeter —") {
		t.Fatalf("help: code=%d output=%q", code, out.String())
	}
}
