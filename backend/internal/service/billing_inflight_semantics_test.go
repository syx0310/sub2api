//go:build unit

package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestInflightEstimateGPT61SubscriptionLongContextMatchesSettlement(t *testing.T) {
	for _, withResolver := range []bool{false, true} {
		for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken, AccountTypeAPIKey, "unknown"} {
			for _, tier := range []string{"", "priority", "ultrafast"} {
				for _, tokens := range []int{272000, 272001, 500000} {
					t.Run(fmt.Sprintf("resolver=%t/%s/%s/%d", withResolver, accountType, tier, tokens), func(t *testing.T) {
						billing := NewBillingService(&config.Config{}, nil)
						deps := inflightEstimateDeps{billing: billing}
						if withResolver {
							deps.resolver = NewModelPricingResolver(nil, billing)
						}
						var account *Account
						if accountType != "unknown" {
							account = &Account{Platform: PlatformOpenAI, Type: accountType,
								Extra: map[string]any{openAILongContextBillingEnabledKey: true}}
						}
						group := &Group{ID: 1, Platform: PlatformOpenAI, RateMultiplier: 1, LongContextPricingEnabled: true}
						key := &APIKey{User: &User{ID: 1}, GroupID: &group.ID, Group: group}
						req := InflightEstimateRequest{Model: "gpt-6.1-sol", UpstreamModel: "gpt-6.1-sol", BodyBytes: tokens * 4,
							MaxTokens: 1000, ServiceTier: tier, Account: account}
						estimate, priced := deps.estimate(context.Background(), key, req)
						require.True(t, priced)
						inputFactor, outputFactor, tierFactor := 1.0, 1.0, 1.0
						if tokens > 272000 && (accountType == AccountTypeAPIKey || accountType == "unknown") {
							inputFactor, outputFactor = 2, 1.5
						}
						if tier == "priority" {
							tierFactor = 2
						} else if tier == "ultrafast" {
							tierFactor = 6
						}
						require.InDelta(t, (float64(tokens)*2e-6*inputFactor+1000*10e-6*outputFactor)*tierFactor, estimate, 1e-9)
						svc := &OpenAIGatewayService{billingService: billing, resolver: deps.resolver}
						settled, err := svc.calculateOpenAIRecordUsageTokenCost(context.Background(), key, req.Model, 1,
							OpenAIPricingAtFromContext(context.Background()), UsageTokens{InputTokens: tokens, OutputTokens: 1000},
							tier, "", openAILongContextBillingGate(account), false,
							openAISubscriptionLongContextExempt(account, &OpenAIForwardResult{Model: req.Model, UpstreamModel: req.UpstreamModel}))
						require.NoError(t, err)
						require.InDelta(t, settled.ActualCost, estimate, 1e-9)
					})
				}
			}
		}
	}
}

func TestInflightEstimateGPT61ExemptionUsesForwardedModel(t *testing.T) {
	billing := NewBillingService(&config.Config{}, nil)
	deps := inflightEstimateDeps{billing: billing, resolver: NewModelPricingResolver(nil, billing)}
	group := &Group{ID: 1, Platform: PlatformOpenAI, RateMultiplier: 1, LongContextPricingEnabled: true}
	key := &APIKey{User: &User{ID: 1}, GroupID: &group.ID, Group: group}
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Extra: map[string]any{openAILongContextBillingEnabledKey: true}}
	for _, tc := range []struct {
		model, upstream string
		want            float64
	}{
		{"gpt-6-sol", "gpt-6.1-sol", .61},
		{"gpt-6.1-sol", "gpt-6-sol", 1.215},
		{"unpriced-public-alias", "gpt-6.1-sol", .61},
	} {
		t.Run(tc.model+"/"+tc.upstream, func(t *testing.T) {
			cost, priced := deps.estimate(context.Background(), key, InflightEstimateRequest{
				Model: tc.model, UpstreamModel: tc.upstream, Account: account, BodyBytes: 300000 * 4, MaxTokens: 1000,
			})
			require.True(t, priced)
			require.InDelta(t, tc.want, cost, 1e-9)
		})
	}
}

func TestInflightEstimateFinalTierEffortAndExplicitZero(t *testing.T) {
	for _, zero := range []bool{false, true} {
		input, output := 1e-6, 2e-6
		if zero {
			input, output = 0, 0
		}
		billing, resolver := newTokenCostTestEnv(t, PlatformOpenAI, []ChannelModelPricing{{
			Platform: PlatformOpenAI, Models: []string{"public"}, BillingMode: BillingModeToken,
			InputPrice: &input, OutputPrice: &output,
			ReasoningEffortMultipliers: map[string]float64{"low": 3},
		}}, nil)
		group := &Group{ID: 100, Platform: PlatformOpenAI, RateMultiplier: 1}
		deps := inflightEstimateDeps{billing: billing, resolver: resolver}
		for _, tier := range []string{"", "fast", "flex"} {
			cost, priced := deps.estimate(context.Background(), &APIKey{User: &User{ID: 1}, GroupID: &group.ID, Group: group}, InflightEstimateRequest{
				Model: "public", UpstreamModel: "gpt-6.1-sol", BodyBytes: 4000, MaxTokens: 1000,
				ServiceTier: tier, ReasoningEffort: "none",
			})
			require.True(t, priced, "an explicit free price is still priced")
			multiplier := 1.0
			if tier == "fast" {
				multiplier = 2
			}
			if tier == "flex" {
				multiplier = .5
			}
			require.InDelta(t, 1000*(input+output)*3*multiplier, cost, 1e-12)
		}
	}
}

func TestInflightEstimateAstraUltrafastAndLongContextGates(t *testing.T) {
	billing := NewBillingService(&config.Config{}, nil)
	deps := inflightEstimateDeps{billing: billing, resolver: NewModelPricingResolver(nil, billing)}
	for _, groupEnabled := range []bool{false, true} {
		for _, accountEnabled := range []bool{false, true} {
			for _, n := range []int{272000, 272001} {
				group := &Group{ID: 1, Platform: PlatformOpenAI, RateMultiplier: 1, LongContextPricingEnabled: groupEnabled}
				account := &Account{Platform: PlatformOpenAI, Extra: map[string]any{"openai_long_context_billing_enabled": accountEnabled}}
				cost, priced := deps.estimate(context.Background(), &APIKey{User: &User{ID: 1}, GroupID: &group.ID, Group: group}, InflightEstimateRequest{
					Model: "gpt-6-astra", BodyBytes: 4 * n, MaxTokens: 1000, ServiceTier: "ultrafast", Account: account,
				})
				require.True(t, priced)
				im, om := 1.0, 1.0
				if n > 272000 && groupEnabled && accountEnabled {
					im, om = 2, 1.5
				}
				require.InDelta(t, float64(n)*60e-6*im+1000*300e-6*om, cost, 1e-9)
			}
		}
	}
}

func TestInflightEstimateActualMappedModelAvoidsSnapshotLookup(t *testing.T) {
	deps := inflightEstimateDeps{
		billing: NewBillingService(&config.Config{}, nil),
		accountMappedModels: func(context.Context, *APIKey, string) []string {
			t.Fatal("selected WS account must not scan candidates")
			return nil
		},
	}
	cost, priced := deps.estimate(context.Background(), &APIKey{User: &User{ID: 1}}, InflightEstimateRequest{
		Model: "unpriced-public-alias", UpstreamModel: "gpt-6.1-sol", Account: &Account{Platform: PlatformOpenAI}, MaxTokens: 100,
	})
	require.True(t, priced)
	require.InDelta(t, .001, cost, 1e-12)
}
