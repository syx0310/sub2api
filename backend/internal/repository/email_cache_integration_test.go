//go:build integration

package repository

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type EmailCacheSuite struct {
	IntegrationRedisSuite
	cache service.EmailCache
}

func (s *EmailCacheSuite) SetupTest() {
	s.IntegrationRedisSuite.SetupTest()
	s.cache = NewEmailCache(s.rdb)
}

func (s *EmailCacheSuite) TestGetVerificationCode_Missing() {
	_, err := s.cache.GetVerificationCode(s.ctx, "nonexistent@example.com")
	require.True(s.T(), errors.Is(err, redis.Nil), "expected redis.Nil for missing verification code")
}

func (s *EmailCacheSuite) TestSetAndGetVerificationCode() {
	email := "a@example.com"
	emailTTL := 2 * time.Minute
	data := &service.VerificationCodeData{Code: "123456", Attempts: 1, CreatedAt: time.Now()}

	require.NoError(s.T(), s.cache.SetVerificationCode(s.ctx, email, data, emailTTL), "SetVerificationCode")

	got, err := s.cache.GetVerificationCode(s.ctx, email)
	require.NoError(s.T(), err, "GetVerificationCode")
	require.Equal(s.T(), "123456", got.Code)
	require.Equal(s.T(), 1, got.Attempts)
}

func (s *EmailCacheSuite) TestVerificationCode_TTL() {
	email := "ttl@example.com"
	emailTTL := 2 * time.Minute
	data := &service.VerificationCodeData{Code: "654321", Attempts: 0, CreatedAt: time.Now()}

	require.NoError(s.T(), s.cache.SetVerificationCode(s.ctx, email, data, emailTTL), "SetVerificationCode")

	emailKey := verifyCodeKeyPrefix + email
	ttl, err := s.rdb.TTL(s.ctx, emailKey).Result()
	require.NoError(s.T(), err, "TTL emailKey")
	s.AssertTTLWithin(ttl, 1*time.Second, emailTTL)
}

func (s *EmailCacheSuite) TestDeleteVerificationCode() {
	email := "delete@example.com"
	data := &service.VerificationCodeData{Code: "999999", Attempts: 0, CreatedAt: time.Now()}

	require.NoError(s.T(), s.cache.SetVerificationCode(s.ctx, email, data, 2*time.Minute), "SetVerificationCode")

	// Verify it exists
	_, err := s.cache.GetVerificationCode(s.ctx, email)
	require.NoError(s.T(), err, "GetVerificationCode before delete")

	// Delete
	require.NoError(s.T(), s.cache.DeleteVerificationCode(s.ctx, email), "DeleteVerificationCode")

	// Verify it's gone
	_, err = s.cache.GetVerificationCode(s.ctx, email)
	require.True(s.T(), errors.Is(err, redis.Nil), "expected redis.Nil after delete")
}

func (s *EmailCacheSuite) TestDeleteVerificationCode_NonExistent() {
	// Deleting a non-existent key should not error
	require.NoError(s.T(), s.cache.DeleteVerificationCode(s.ctx, "nonexistent@example.com"), "DeleteVerificationCode non-existent")
}

func (s *EmailCacheSuite) TestGetVerificationCode_JSONCorruption() {
	emailKey := verifyCodeKeyPrefix + "corrupted@example.com"

	require.NoError(s.T(), s.rdb.Set(s.ctx, emailKey, "not-json", 1*time.Minute).Err(), "Set invalid JSON")

	_, err := s.cache.GetVerificationCode(s.ctx, "corrupted@example.com")
	require.Error(s.T(), err, "expected error for corrupted JSON")
	require.False(s.T(), errors.Is(err, redis.Nil), "expected decoding error, not redis.Nil")
}

func TestEmailCacheSuite(t *testing.T) {
	suite.Run(t, new(EmailCacheSuite))
}

func (s *EmailCacheSuite) TestConcurrentVerificationAttemptsAndTTL() {
	email := "atomic-attempts@example.com"
	require.NoError(s.T(), s.cache.SetVerificationCode(s.ctx, email, &service.VerificationCodeData{Code: "123456"}, time.Minute))
	svc := service.NewEmailService(nil, s.cache)
	errs := make(chan error, 32)
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- svc.VerifyCode(s.ctx, email, "000000")
		}()
	}
	wg.Wait()
	close(errs)
	invalid := 0
	for err := range errs {
		if errors.Is(err, service.ErrInvalidVerifyCode) {
			invalid++
		} else {
			require.ErrorIs(s.T(), err, service.ErrVerifyCodeMaxAttempts)
		}
	}
	require.LessOrEqual(s.T(), invalid, 4)
	require.ErrorIs(s.T(), svc.VerifyCode(s.ctx, email, "123456"), service.ErrVerifyCodeMaxAttempts)
	ttl, err := s.rdb.PTTL(s.ctx, verifyCodeKey(email)+attemptsKeySuffix).Result()
	require.NoError(s.T(), err)
	s.AssertTTLWithin(ttl, time.Second, time.Minute)
	require.NoError(s.T(), s.cache.SetVerificationCode(s.ctx, email, &service.VerificationCodeData{Code: "654321"}, time.Minute))
	require.NoError(s.T(), svc.VerifyCode(s.ctx, email, "654321"))
}

func (s *EmailCacheSuite) TestResetTokenConsumedExactlyOnce() {
	email := "atomic-reset@example.com"
	require.NoError(s.T(), s.cache.SetPasswordResetToken(s.ctx, email, &service.PasswordResetTokenData{Token: "token-hash"}, time.Minute))
	consumed, err := s.cache.ConsumePasswordResetToken(s.ctx, email, "wrong-hash")
	require.NoError(s.T(), err)
	require.False(s.T(), consumed)
	var accepted atomic.Int32
	errs := make(chan error, 32)
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := s.cache.ConsumePasswordResetToken(s.ctx, email, "token-hash")
			if ok {
				accepted.Add(1)
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(s.T(), err)
	}
	require.Equal(s.T(), int32(1), accepted.Load())
	_, err = s.cache.GetPasswordResetToken(s.ctx, email)
	require.ErrorIs(s.T(), err, redis.Nil)
}
