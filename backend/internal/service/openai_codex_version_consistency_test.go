//go:build unit

package service

import (
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

func TestCodexVersionConstants_Consistency(t *testing.T) {
	require.True(t, strings.Contains(codexCLIUserAgent, openai.CodexDefaultOriginator+"/"+codexCLIVersion),
		"codexCLIUserAgent must embed codexCLIVersion")

	require.True(t, strings.Contains(DefaultOpenAICodexUserAgent, codexCLIVersion),
		"DefaultOpenAICodexUserAgent must embed codexCLIVersion")
	require.Equal(t,
		"codex-tui/0.155.1 (Ubuntu 24.4.0; x86_64) xterm-256color (codex-tui; 0.155.1)",
		codexCLIUserAgent,
		"default outbound Codex identity must match the supported TUI fingerprint",
	)
}
