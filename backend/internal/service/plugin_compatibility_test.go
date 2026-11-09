package service

import (
	"testing"

	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluatePluginCompatibility(t *testing.T) {
	manifest := testPluginManifest(nil)
	host := PluginHostInfo{Version: "0.1.179", BuildType: "release"}

	result := EvaluatePluginCompatibility(manifest, host)
	require.True(t, result.Compatible)
	assert.True(t, result.Tested)
	assert.Equal(t, "compatible", result.Status)

	manifest.Requires.TestedSub2APIVersions = []string{"0.1.178"}
	result = EvaluatePluginCompatibility(manifest, host)
	require.True(t, result.Compatible)
	assert.False(t, result.Tested)
	assert.Equal(t, "untested", result.Status)

	manifest.Requires.Sub2API = ">=0.2.0 <0.3.0"
	result = EvaluatePluginCompatibility(manifest, host)
	assert.False(t, result.Compatible)
	assert.Equal(t, "incompatible", result.Status)
}

func TestEvaluatePluginCompatibilityRejectsProtocolMismatch(t *testing.T) {
	manifest := testPluginManifest(nil)
	manifest.Requires.PluginProtocol = pluginv1.ProtocolVersion + 1

	result := EvaluatePluginCompatibility(manifest, PluginHostInfo{Version: "0.1.179"})

	assert.False(t, result.Compatible)
	assert.Equal(t, "incompatible", result.Status)
}

func TestMatchesSemverRange(t *testing.T) {
	assert.True(t, matchesSemverRange("0.1.179", ">=0.1.170, <0.2.0"))
	assert.True(t, matchesSemverRange("v1.2.3", "=1.2.3"))
	assert.False(t, matchesSemverRange("0.1.169", ">=0.1.170 <0.2.0"))
	assert.False(t, matchesSemverRange("dev", ">=0.1.0"))
	assert.False(t, matchesSemverRange("0.1.179", "^0.1.0"))
}

func TestPluginHostPostReleaseCompatibility(t *testing.T) {
	manifest := testPluginManifest(nil)
	manifest.Requires.Sub2API = ">=0.2.19 <0.2.20"
	manifest.Requires.TestedSub2APIVersions = []string{"0.2.19"}
	host := PluginHostInfo{Version: "v0.2.19p1"}
	result := EvaluatePluginCompatibility(manifest, host)
	require.True(t, result.Compatible)
	require.Equal(t, "untested", result.Status)
	manifest.Requires.TestedSub2APIVersions = []string{"0.2.19p1"}
	require.True(t, EvaluatePluginCompatibility(manifest, host).Tested)
	manifest.Requires.Sub2API = "=0.2.19"
	require.False(t, EvaluatePluginCompatibility(manifest, host).Compatible)
	require.True(t, matchesSemverRange("0.2.19p10", ">=0.2.19p2 <0.2.20"))
	require.False(t, matchesSemverRange("0.2.19p2", ">=0.2.19p10"))
	require.Empty(t, normalizeSemver("0.2.19p1"), "plugin package versions remain standard SemVer")
}
