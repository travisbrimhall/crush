package tools

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEffectiveBannedCommands(t *testing.T) {
	tests := []struct {
		name           string
		unbanCommands  []string
		shouldBeBanned []string
		shouldBeAllow  []string
	}{
		{
			name:           "nil unban returns full list",
			unbanCommands:  nil,
			shouldBeBanned: []string{"curl", "ssh", "wget", "sudo"},
			shouldBeAllow:  nil,
		},
		{
			name:           "empty unban returns full list",
			unbanCommands:  []string{},
			shouldBeBanned: []string{"curl", "ssh", "wget", "sudo"},
			shouldBeAllow:  nil,
		},
		{
			name:           "unban ssh removes it",
			unbanCommands:  []string{"ssh"},
			shouldBeBanned: []string{"curl", "wget", "sudo"},
			shouldBeAllow:  []string{"ssh"},
		},
		{
			name:           "unban multiple commands",
			unbanCommands:  []string{"ssh", "scp", "curl", "wget", "rsync"},
			shouldBeBanned: []string{"sudo", "telnet"},
			shouldBeAllow:  []string{"ssh", "scp", "curl", "wget"},
		},
		{
			name:           "unban nonexistent command is no-op",
			unbanCommands:  []string{"doesnotexist"},
			shouldBeBanned: []string{"curl", "ssh", "wget", "sudo"},
			shouldBeAllow:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := effectiveBannedCommands(tt.unbanCommands)

			for _, cmd := range tt.shouldBeBanned {
				require.True(t, slices.Contains(result, cmd),
					"Expected %q to be in banned list", cmd)
			}

			for _, cmd := range tt.shouldBeAllow {
				require.False(t, slices.Contains(result, cmd),
					"Expected %q to NOT be in banned list", cmd)
			}
		})
	}
}
