package config

import "testing"

func TestSplitSourceRef(t *testing.T) {
	tests := []struct {
		ref, source, name string
	}{
		{"corp:devx", "corp", "devx"},
		{"devx", "", "devx"},
		{"corp:a:b", "corp", "a:b"}, // first colon only; the remainder fails validation downstream
		{":devx", "", "devx"},       // empty source = local, same as no prefix
		{"corp:", "corp", ""},       // empty name; caller's name validation refuses it
		{"", "", ""},
	}
	for _, tt := range tests {
		s, n := SplitSourceRef(tt.ref)
		if s != tt.source || n != tt.name {
			t.Errorf("SplitSourceRef(%q) = (%q, %q), want (%q, %q)", tt.ref, s, n, tt.source, tt.name)
		}
	}
}

func TestValidateSourceName(t *testing.T) {
	if err := ValidateSourceName("corp"); err != nil {
		t.Errorf("ValidateSourceName(corp): %v", err)
	}
	for _, bad := range []string{"", "co.rp", "-corp", "co/rp", "..", "co:rp"} {
		if err := ValidateSourceName(bad); err == nil {
			t.Errorf("ValidateSourceName(%q): expected an error", bad)
		}
	}
}
