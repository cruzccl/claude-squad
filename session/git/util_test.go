package git

import (
	"testing"
)

func TestSanitizeBranchName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple lowercase string",
			input:    "feature",
			expected: "feature",
		},
		{
			name:     "string with spaces",
			input:    "new feature branch",
			expected: "new-feature-branch",
		},
		{
			name:     "mixed case string",
			input:    "FeAtUrE BrAnCh",
			expected: "feature-branch",
		},
		{
			name:     "string with special characters",
			input:    "feature!@#$%^&*()",
			expected: "feature",
		},
		{
			name:     "string with allowed special characters",
			input:    "feature/sub_branch.v1",
			expected: "feature/sub_branch.v1",
		},
		{
			name:     "string with multiple dashes",
			input:    "feature---branch",
			expected: "feature-branch",
		},
		{
			name:     "string with leading and trailing dashes",
			input:    "-feature-branch-",
			expected: "feature-branch",
		},
		{
			name:     "string with leading and trailing slashes",
			input:    "/feature/branch/",
			expected: "feature/branch",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "complex mixed case with special chars",
			input:    "USER/Feature Branch!@#$%^&*()/v1.0",
			expected: "user/feature-branch/v1.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeBranchName(tt.input)
			if got != tt.expected {
				t.Errorf("sanitizeBranchName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestSuggestBranchName(t *testing.T) {
	tests := []struct {
		name     string
		prefix   string
		title    string
		expected string
	}{
		{
			name:     "prefix is part of the suggestion",
			prefix:   "carlos/",
			title:    "New feature",
			expected: "carlos/new-feature",
		},
		{
			name:     "accents are dropped rather than transliterated",
			prefix:   "carlos/",
			title:    "Mi Sesión Nueva",
			expected: "carlos/mi-sesin-nueva",
		},
		{
			name:     "a title that sanitises to nothing falls back to a readable stem",
			prefix:   "carlos/",
			title:    "🚀🚀🚀",
			expected: "carlos/session",
		},
		{
			name:     "no prefix configured",
			prefix:   "",
			title:    "New feature",
			expected: "new-feature",
		},
		{
			name:     "empty title",
			prefix:   "carlos/",
			title:    "",
			expected: "carlos/session",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SuggestBranchName(tt.prefix, tt.title); got != tt.expected {
				t.Errorf("SuggestBranchName(%q, %q) = %q, want %q", tt.prefix, tt.title, got, tt.expected)
			}
		})
	}
}

func TestValidateBranchName(t *testing.T) {
	valid := []string{"feature", "carlos/new-feature", "carlos/feature/deep", "v1.0"}
	for _, name := range valid {
		if err := ValidateBranchName(name); err != nil {
			t.Errorf("ValidateBranchName(%q) = %v, want nil", name, err)
		}
	}

	invalid := []string{"", "  ", "bad branch", "a..b", "-leading", "trailing.lock", "back\\slash", "@{-1}"}
	for _, name := range invalid {
		if err := ValidateBranchName(name); err == nil {
			t.Errorf("ValidateBranchName(%q) = nil, want an error", name)
		}
	}
}
